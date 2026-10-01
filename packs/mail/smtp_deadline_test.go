package mail

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPAllRecipients(t *testing.T) {
	addr, received := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	p := smtpProvider{host: host, port: port, plain: true, timeout: time.Second}
	msg := attachedMessage()
	m, err := New(Config{Provider: "outbox"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.prepare(context.Background(), &msg); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	log := received()
	for _, address := range []string{"ada@example.com", "lin@example.com", "cc@example.com", "blind@example.com"} {
		if strings.Count(log, "RCPT TO:<"+address+">") != 1 {
			t.Fatal("envelope recipient missing or duplicated", log)
		}
	}
	if strings.Contains(log, "Bcc:") {
		t.Fatal("blind recipients exposed", log)
	}
}

// The one accepted connection is owned until the test cleans it up.
func stalledSMTP(t *testing.T, stage string) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done, reached := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("SMTP peer did not stop")
		}
	})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		stall := func() { close(reached); _, _ = io.Copy(io.Discard, conn) }
		if stage == "greeting" || stage == "tls" {
			stall()
			return
		}
		io.WriteString(conn, "220 fake ESMTP\r\n")
		reader := bufio.NewReader(conn)
		data := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case data:
				if cmd == "." {
					if stage == "data" {
						stall()
						return
					}
					data = false
					io.WriteString(conn, "250 queued\r\n")
				}
			case strings.HasPrefix(cmd, "EHLO"):
				if stage == "starttls" {
					io.WriteString(conn, "250-fake\r\n250 STARTTLS\r\n")
				} else {
					io.WriteString(conn, "250 fake\r\n")
				}
			case cmd == "STARTTLS":
				io.WriteString(conn, "220 begin TLS\r\n")
				stall()
				return
			case cmd == "DATA":
				data = true
				io.WriteString(conn, "354 continue\r\n")
			case cmd == "QUIT":
				stall()
				return
			default:
				io.WriteString(conn, "250 ok\r\n")
			}
		}
	}()
	return listener.Addr().String(), reached
}

func TestSMTPDeadlineAndCancellation(t *testing.T) {
	for _, stage := range []string{"greeting", "tls", "starttls", "data", "quit"} {
		for _, cancellation := range []bool{false, true} {
			name := stage + "/timeout"
			if cancellation {
				name = stage + "/cancel"
			}
			t.Run(name, func(t *testing.T) {
				addr, reached := stalledSMTP(t, stage)
				host, port, _ := net.SplitHostPort(addr)
				p := smtpProvider{host: host, port: port, plain: stage != "starttls", implicitTLS: stage == "tls", timeout: 150 * time.Millisecond}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if cancellation {
					p.timeout = time.Minute
				}
				done := make(chan error, 1)
				go func() {
					_, err := p.Send(ctx, Message{From: "sender@example.com", To: "a@b.c", Subject: "Hi", Text: "hello"})
					done <- err
				}()
				select {
				case <-reached:
				case <-time.After(time.Second):
					cancel()
					t.Fatal("peer stage not reached")
				}
				if cancellation {
					cancel()
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("stalled SMTP succeeded")
					}
				case <-time.After(time.Second):
					cancel()
					<-done
					t.Fatal("SMTP ignored timeout/cancellation")
				}
			})
		}
	}
}
