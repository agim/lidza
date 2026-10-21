package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt in with LIDZA_TEST_CADDY=/path/to/caddy. No production configuration,
// public DNS, ACME or externally reachable listener is used.
func TestRealNativeTLSEdge(t *testing.T) {
	binary := os.Getenv("LIDZA_TEST_CADDY")
	if binary == "" {
		t.Skip("set LIDZA_TEST_CADDY to exercise real TLS terminator")
	}
	ca := newAuthority(t)
	edgeCert := ca.issue(t, "public.example")
	relayCert := ca.issue(t, "relay.internal")
	appCert := ca.issue(t, "app.internal")
	appVerify, err := Verify(Policy{Peers: []string{Pin(relayCert.Leaf)}, Delegates: []string{Pin(edgeCert.Leaf)}, Authorize: allow, Claim: claimStore()})
	if err != nil {
		t.Fatal(err)
	}
	app := privateServer(t, ca, appCert, appVerify(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !RequireTLS13(r) {
			http.Error(w, "original TLS 1.3 required", 403)
			return
		}
		e, _ := From(r.Context())
		if len(e.Gateways) != 2 {
			t.Error("wrong protected hop count")
		}
		_, _ = w.Write([]byte("protected gateway accepted"))
	})))
	relayVerify, err := Verify(Policy{Peers: []string{Pin(edgeCert.Leaf)}, NativeEdges: []string{Pin(edgeCert.Leaf)}, Authorize: allow, Claim: claimStore()})
	if err != nil {
		t.Fatal(err)
	}
	relay := privateServer(t, ca, relayCert, relayVerify(secureProxy(t, app.URL, ca, relayCert, "app.internal")))
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	certPath := write("edge.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: edgeCert.Certificate[0]}))
	keyDER, err := x509.MarshalPKCS8PrivateKey(edgeCert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := write("edge-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	caPath := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(addr)
	config := fmt.Sprintf(`{
 admin off
 auto_https off
}
https://public.example:%s {
 bind 127.0.0.1
 tls %s %s
 reverse_proxy %s {
  header_up Lidza-Gateway-Transport edge-headers-v1
  header_up Lidza-Gateway-Tls-Version {http.request.tls.version}
  header_up Lidza-Gateway-Authority {http.request.hostport}
  header_up Lidza-Gateway-Server-Name {http.request.tls.server_name}
  header_up Lidza-Gateway-Method {http.request.method}
  header_up Lidza-Gateway-Target {http.request.uri}
  header_up Lidza-Gateway-Issued {time.now.unix}
  header_up Lidza-Gateway-Nonce {http.request.uuid}
  transport http {
   tls_server_name relay.internal
   tls_trust_pool file %s
   tls_client_auth %s %s
  }
 }
}
`, port, certPath, keyPath, relay.URL, caPath, certPath, keyPath)
	configPath := write("Caddyfile", []byte(config))
	logPath := filepath.Join(dir, "caddy.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "run", "--config", configPath, "--adapter", "caddyfile")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_DATA_HOME="+filepath.Join(dir, "data"))
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	for deadline := time.Now().Add(5 * time.Second); ; {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(logPath)
			t.Fatalf("Caddy did not listen: %s", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, version := range []uint16{tls.VersionTLS13, tls.VersionTLS12} {
		req, _ := http.NewRequest("POST", "https://"+addr+"/protected?x=1", strings.NewReader("body"))
		req.Host = "public.example"
		req.Header.Set(Header, "forged")
		for _, name := range EdgeHeaderNames() {
			req.Header.Set(name, "attacker controlled")
		}
		req.Header.Set("X-Forwarded-Proto", "https")
		response, err := publicClient(t, ca, version).Do(req)
		if err != nil {
			data, _ := os.ReadFile(logPath)
			t.Fatalf("real TLS request failed: %v; %s", err, data)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		want := 200
		if version == tls.VersionTLS12 {
			want = 403
		}
		if response.StatusCode != want {
			data, _ := os.ReadFile(logPath)
			t.Fatalf("TLS%x response=%d body=%s logs=%s", version, response.StatusCode, body, data)
		}
	}
	t.Log("real native TLS edge -> authenticated TLS relay -> authenticated TLS application passed; forged client headers overwritten; original TLS1.2 rejected despite TLS1.3 backend hops")
}
