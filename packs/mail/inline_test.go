package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func inlineMessage() Message {
	msg := attachedMessage()
	msg.ReplyTo = `"Support, One" <one@example.com>, Two <two@example.com>`
	msg.HTML = `<p>hello</p><img src="cid:logo@message"><img src="cid:empty">`
	msg.Headers = map[string]string{"X-Tag": "receipt"}
	msg.Attachments = append(msg.Attachments,
		Attachment{Name: "logo.png", ContentType: "image/png", ContentID: "logo@message", Data: []byte{0, 255, 17, 128}},
		Attachment{Name: "empty.png", ContentType: "image/png", ContentID: "empty"})
	return msg
}

func TestInlineValidation(t *testing.T) {
	m, err := New(Config{Provider: "outbox", MaxRecipients: 4}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Message){
		"no HTML":               func(m *Message) { m.HTML = "" },
		"angle ID":              func(m *Message) { m.Attachments[2].ContentID = "<logo>" },
		"space ID":              func(m *Message) { m.Attachments[2].ContentID = "bad id" },
		"injected ID":           func(m *Message) { m.Attachments[2].ContentID = "bad\r\nBcc:x@y.z" },
		"prefix ID":             func(m *Message) { m.Attachments[2].ContentID = "cid:logo" },
		"unicode ID":            func(m *Message) { m.Attachments[2].ContentID = "lögö" },
		"long ID":               func(m *Message) { m.Attachments[2].ContentID = strings.Repeat("a", 128) },
		"duplicate ID":          func(m *Message) { m.Attachments[3].ContentID = m.Attachments[2].ContentID },
		"Reply-To duplicate":    func(m *Message) { m.ReplyTo = "a@b.c, a@b.c" },
		"Reply-To invalid":      func(m *Message) { m.ReplyTo += ", invalid" },
		"Reply-To count":        func(m *Message) { m.ReplyTo = "a@b.c,b@b.c,c@b.c,d@b.c,e@b.c" },
		"Reply-To bytes":        func(m *Message) { m.ReplyTo = strings.Repeat("a", 65537) },
		"header case duplicate": func(m *Message) { m.Headers["x-tag"] = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			msg := inlineMessage()
			change(&msg)
			if _, err := m.Send(context.Background(), msg); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
	msg := inlineMessage()
	msg.Attachments[2].ContentID = strings.Repeat("a", 127)
	if err := m.prepare(context.Background(), &msg); err != nil {
		t.Fatal(err)
	}
}

// inspectInlineMIME decodes real MIME recursively, including related resources
// inside the HTML alternative. Content IDs and filenames are independent.
func inspectInlineMIME(t *testing.T, data []byte, want Message) {
	t.Helper()
	parsed, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("Bcc") != "" || strings.Contains(string(data), "blind@example.com") {
		t.Fatal("Bcc leaked into MIME")
	}
	reply, err := parsed.Header.AddressList("Reply-To")
	if err != nil || len(reply) != 2 || reply[0].Name != "Support, One" || reply[1].Address != "two@example.com" {
		t.Fatal("Reply-To changed", reply, err)
	}
	if parsed.Header.Get("X-Tag") != "receipt" {
		t.Fatal("custom header lost")
	}
	files := map[string]Attachment{}
	for _, file := range want.Attachments {
		files[file.Name] = file
	}
	seen := map[string]bool{}
	var types []string
	var walk func(textproto.MIMEHeader, io.Reader)
	walk = func(h textproto.MIMEHeader, body io.Reader) {
		kind, params, err := mime.ParseMediaType(h.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, kind)
		if strings.HasPrefix(kind, "multipart/") {
			reader := multipart.NewReader(body, params["boundary"])
			for {
				part, err := reader.NextRawPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				walk(part.Header, part)
			}
			return
		}
		if h.Get("Content-Disposition") == "" {
			decoded, err := io.ReadAll(quotedprintable.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			expected := want.Text
			if kind == "text/html" {
				expected = want.HTML
			}
			if string(decoded) != expected {
				t.Fatal("body changed", kind, string(decoded))
			}
			return
		}
		disposition, parameters, err := mime.ParseMediaType(h.Get("Content-Disposition"))
		if err != nil {
			t.Fatal(err)
		}
		name := parameters["filename"]
		file, ok := files[name]
		if !ok || seen[name] {
			t.Fatal("unknown or duplicated file", name)
		}
		seen[name] = true
		id, expectedDisposition := "", "attachment"
		if file.ContentID != "" {
			id, expectedDisposition = "<"+file.ContentID+">", "inline"
		}
		if disposition != expectedDisposition || h.Get("Content-ID") != id {
			t.Fatal("file identity changed", h)
		}
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, body))
		if err != nil || !bytes.Equal(decoded, file.Data) {
			t.Fatal("file bytes changed", name, err)
		}
	}
	walk(textproto.MIMEHeader(parsed.Header), parsed.Body)
	if len(seen) != len(files) {
		t.Fatal("missing MIME files", seen)
	}
	var expected []string
	regular := false
	for _, file := range want.Attachments {
		regular = regular || file.ContentID == ""
	}
	if regular {
		expected = append(expected, "multipart/mixed")
	}
	if want.Text != "" {
		expected = append(expected, "multipart/alternative", "text/plain")
	}
	expected = append(expected, "multipart/related", "text/html", "image/png", "image/png")
	if regular {
		expected = append(expected, "text/plain", "application/octet-stream")
	}
	if !reflect.DeepEqual(types, expected) {
		t.Fatal("wrong MIME tree", types, expected)
	}
}

func TestInlineMIME(t *testing.T) {
	for _, htmlOnly := range []bool{false, true} {
		for _, inlineOnly := range []bool{false, true} {
			msg := inlineMessage()
			if htmlOnly {
				msg.Text = ""
			}
			if inlineOnly {
				msg.Attachments = msg.Attachments[2:]
			}
			m, _ := New(Config{Provider: "outbox"}, nil, nil)
			if err := m.prepare(context.Background(), &msg); err != nil {
				t.Fatal(err)
			}
			from, _ := parseAddress(msg.From)
			first, _ := parseAddress("ada@example.com")
			data, _, err := buildMIME(msg, from, first)
			if err != nil {
				t.Fatal(err)
			}
			inspectInlineMIME(t, data, msg)
		}
	}
}

func TestProvidersInlineAndReplyTo(t *testing.T) {
	for _, provider := range []string{"mailgun", "sendgrid", "postmark", "resend"} {
		t.Run(provider, func(t *testing.T) {
			msg := inlineMessage()
			mimePayload := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if provider == "mailgun" {
					if r.URL.Path != "/v3/example.com/messages.mime" {
						t.Error("wrong MIME endpoint", r.URL.Path)
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					if !reflect.DeepEqual(r.MultipartForm.Value["to"], []string{"ada@example.com", "lin@example.com", "cc@example.com", "blind@example.com"}) {
						t.Error("envelope changed", r.MultipartForm.Value)
					}
					f, _, err := r.FormFile("message")
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer f.Close()
					data, err := io.ReadAll(f)
					if err != nil {
						t.Error(err)
						return
					}
					mimePayload <- data
				} else {
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
						return
					}
					key := "attachments"
					if provider == "postmark" {
						key = "Attachments"
					}
					var files []map[string]any
					if err := json.Unmarshal(payload[key], &files); err != nil {
						t.Error(err)
						return
					}
					if len(files) != 4 {
						t.Error("files lost")
						return
					}
					idKey, id := "content_id", "logo@message"
					if provider == "postmark" {
						idKey, id = "ContentID", "cid:logo@message"
					}
					if files[2][idKey] != id || files[0][idKey] != nil {
						t.Error("inline identity changed", files)
					}
					if provider == "sendgrid" {
						if files[2]["disposition"] != "inline" || files[0]["disposition"] != "attachment" || payload["reply_to"] != nil {
							t.Error("inline/reply mapping changed")
						}
						var reply []map[string]string
						if err := json.Unmarshal(payload["reply_to_list"], &reply); err != nil || len(reply) != 2 || reply[0]["name"] != "Support, One" {
							t.Error("reply list lost", reply, err)
						}
					} else if provider == "resend" {
						var reply []string
						if err := json.Unmarshal(payload["reply_to"], &reply); err != nil || len(reply) != 2 || !strings.Contains(reply[0], "one@example.com") {
							t.Error("reply list lost", reply, err)
						}
					} else {
						var reply string
						if err := json.Unmarshal(payload["ReplyTo"], &reply); err != nil || reply != msg.ReplyTo {
							t.Error("reply list lost", reply, err)
						}
					}
					for i, file := range files {
						dataKey := "content"
						if provider == "postmark" {
							dataKey = "Content"
						}
						encoded, ok := file[dataKey].(string)
						if !ok {
							t.Error("file is not a base64 string", i)
							continue
						}
						data, err := base64.StdEncoding.DecodeString(encoded)
						if err != nil || !bytes.Equal(data, msg.Attachments[i].Data) {
							t.Error("file corrupted", i, err)
						}
					}
				}
				w.Header().Set("X-Message-Id", "sent")
				io.WriteString(w, `{"id":"sent","MessageID":"sent"}`)
			}))
			defer server.Close()
			m, err := New(Config{Provider: provider, APIKey: "test", Domain: "example.com", BaseURL: server.URL}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Send(ctxWith(t, server), msg); err != nil {
				t.Fatal(err)
			}
			if provider == "mailgun" {
				if err := m.prepare(context.Background(), &msg); err != nil {
					t.Fatal(err)
				}
				inspectInlineMIME(t, <-mimePayload, msg)
			}
		})
	}
}

func TestSMTPInline(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	portN, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(Config{Provider: "smtp", SMTPHost: host, SMTPPort: portN, SMTPSecurity: "none"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := inlineMessage()
	if _, err := m.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	log := got()
	for _, recipient := range []string{"ada@example.com", "lin@example.com", "cc@example.com", "blind@example.com"} {
		if !strings.Contains(log, "RCPT TO:<"+recipient+">") {
			t.Fatal("recipient lost", log)
		}
	}
	start := strings.Index(log, "DATA\r\n")
	end := strings.LastIndex(log, "\r\n.\r\n")
	if start < 0 || end < start {
		t.Fatal("SMTP body missing", log)
	}
	if err := m.prepare(context.Background(), &msg); err != nil {
		t.Fatal(err)
	}
	inspectInlineMIME(t, []byte(log[start+len("DATA\r\n"):end]), msg)
}
