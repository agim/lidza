package lidza

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/agim/lidza/pkg/gateway"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestPrivateTLSRejectsUnsafeConfigurationBeforeBoot(t *testing.T) {
	t.Setenv(EnvTLSDomains, "")
	for _, cfg := range []*tls.Config{
		{},
		{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: x509.NewCertPool()},
		{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert, ClientCAs: x509.NewCertPool()},
		{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert},
		{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: x509.NewCertPool(), Certificates: []tls.Certificate{{}}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return nil, nil }},
	} {
		started := false
		err := Serve(context.Background(), App{PrivateTLS: cfg, OnStart: func(context.Context, *Services) error { started = true; return nil }})
		if err == nil || started {
			t.Fatal("unsafe private TLS configuration reached boot")
		}
	}
	t.Setenv(EnvTLSDomains, "public.example")
	if err := Serve(context.Background(), App{PrivateTLS: &tls.Config{}}); err == nil {
		t.Fatal("conflicting ACME and private TLS accepted")
	}
}

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newAuthority(t *testing.T) authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return authority{cert, key, pool}
}
func (a authority) issue(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	cert.Leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestServePrivateTLS(t *testing.T) {
	for _, dual := range []bool{false, true} {
		t.Run(fmt.Sprintf("secondary=%v", dual), func(t *testing.T) { testServePrivateTLS(t, dual) })
	}
}
func testServePrivateTLS(t *testing.T, dual bool) {
	t.Setenv(EnvTLSDomains, "")
	ca := newAuthority(t)
	serverCert := ca.issue(t, "backend.internal")
	clientCert := ca.issue(t, "relay.internal")
	cfg, err := gateway.ServerTLS(serverCert, ca.pool)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	t.Setenv("LIDZA_ADDR", addr)
	tlsAddr := addr
	secondary := ""
	if dual {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		secondary = ln.Addr().String()
		ln.Close()
		tlsAddr = secondary
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, App{PrivateTLS: cfg, PrivateTLSAddr: secondary, OnReady: func() { close(ready) }})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("private TLS listener did not start")
	}
	transport, err := gateway.ClientTransport(clientCert, ca.pool, "backend.internal")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	resp, err := client.Get("https://" + tlsAddr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	noCert := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca.pool, ServerName: "backend.internal", MinVersion: tls.VersionTLS13}}
	defer noCert.CloseIdleConnections()
	if resp, err := (&http.Client{Transport: noCert, Timeout: time.Second}).Get("https://" + tlsAddr + "/healthz"); err == nil {
		resp.Body.Close()
		t.Fatal("unauthenticated TLS client accepted")
	}
	if resp, err := (&http.Client{Timeout: time.Second}).Get("http://" + tlsAddr + "/healthz"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatal("plaintext fallback accepted")
		}
	}
	if dual {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + addr + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("ordinary internal health listener unavailable")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("private TLS listener did not shut down")
	}
}
