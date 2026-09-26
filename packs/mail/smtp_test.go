package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a server without STARTTLS that records what it receives.
func fakeSMTP(t *testing.T) (addr string, got func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var log strings.Builder
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				write := func(s string) { c.Write([]byte(s + "\r\n")) }
				write("220 fake ESMTP")
				data := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					mu.Lock()
					log.WriteString(line)
					mu.Unlock()
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case data:
						if cmd == "." {
							data = false
							write("250 queued as 42")
						}
					case strings.HasPrefix(cmd, "EHLO"):
						write("250-fake\r\n250 AUTH PLAIN")
					case strings.HasPrefix(cmd, "AUTH"):
						write("235 ok")
					case strings.HasPrefix(cmd, "DATA"):
						data = true
						write("354 go on")
					case strings.HasPrefix(cmd, "QUIT"):
						write("221 bye")
						return
					default:
						write("250 ok")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() string { mu.Lock(); defer mu.Unlock(); return log.String() }
}

func TestSMTPFromParts(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	var portN int
	for _, c := range port {
		portN = portN*10 + int(c-'0')
	}
	msg := Message{From: "App <app@example.com>", To: "ana@example.com", Subject: "Hi", Text: "hello"}

	// none: plain text, authenticated.
	p, err := newProvider(Config{Provider: "smtp", SMTPHost: host, SMTPPort: portN, SMTPSecurity: "none", SMTPUsername: "u", SMTPPassword: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if log := got(); !strings.Contains(log, "AUTH PLAIN") || !strings.Contains(log, "RCPT TO:<ana@example.com>") || !strings.Contains(log, "Subject: Hi") {
		t.Fatalf("server saw:\n%s", log)
	}

	// starttls (the default): a server that cannot upgrade is refused
	// before any credentials are sent.
	p, _ = newProvider(Config{Provider: "smtp", SMTPHost: host, SMTPPort: portN, SMTPUsername: "u", SMTPPassword: "secret-2"})
	if _, err := p.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
		t.Fatalf("starttls against a plain server: %v", err)
	}
	if strings.Contains(got(), "secret-2") {
		t.Fatal("credentials sent without TLS")
	}

	// Ports by security; the URL wins over the parts; errors name the fix.
	for security, want := range map[string]string{"starttls": "587", "tls": "465", "none": "25"} {
		p, err := newProvider(Config{Provider: "smtp", SMTPHost: "mail.example.com", SMTPSecurity: security})
		if err != nil || p.(smtpProvider).port != want {
			t.Errorf("%s: %+v %v", security, p, err)
		}
	}
	if p, _ := newProvider(Config{Provider: "smtp", SMTPURL: "smtps://a:b@url.example.com", SMTPHost: "parts.example.com"}); p.(smtpProvider).host != "url.example.com" {
		t.Error("the URL should win over the parts")
	}
	if _, err := newProvider(Config{Provider: "smtp"}); err == nil || !strings.Contains(err.Error(), "MAIL_SMTP_HOST") {
		t.Errorf("no host: %v", err)
	}
	if _, err := newProvider(Config{Provider: "smtp", SMTPHost: "h", SMTPSecurity: "ssl3"}); err == nil {
		t.Error("unknown security accepted")
	}

	// Regions pick the API base; an explicit base wins.
	if p, _ := newProvider(Config{Provider: "mailgun", APIKey: "k", Domain: "d", Region: "eu"}); p.(mailgun).base != "https://api.eu.mailgun.net" {
		t.Errorf("mailgun eu: %s", p.(mailgun).base)
	}
	if p, _ := newProvider(Config{Provider: "sendgrid", APIKey: "k", Region: "EU"}); p.(sendgrid).base != "https://api.eu.sendgrid.com" {
		t.Errorf("sendgrid eu: %s", p.(sendgrid).base)
	}
	if p, _ := newProvider(Config{Provider: "mailgun", APIKey: "k", Domain: "d", Region: "eu", BaseURL: "https://proxy"}); p.(mailgun).base != "https://proxy" {
		t.Error("base should win over region")
	}
}
