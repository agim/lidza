package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"reflect"
	"strings"
	"testing"
)

func attachedMessage() Message {
	return Message{From: "Sender <sender@example.com>", To: "Ada <ada@example.com>, lin@example.com",
		Cc: []string{"cc@example.com"}, Bcc: []string{"blind@example.com"}, Subject: "Résumé", Text: "hello", HTML: "<p>hello</p>",
		Attachments: []Attachment{{Name: "report.txt", Data: []byte("report")}, {Name: "résumé.bin", ContentType: "application/octet-stream", Data: bytes.Repeat([]byte{0, 255, 17}, 100)}}}
}

func TestAttachmentValidation(t *testing.T) {
	m, err := New(Config{Provider: "outbox", MaxRecipients: 4, MaxAttachments: 2, MaxAttachmentBytes: 1024}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := attachedMessage()
	if err := m.prepare(context.Background(), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Attachments[0].ContentType != "text/plain; charset=utf-8" {
		t.Fatal(msg.Attachments[0].ContentType)
	}
	for name, change := range map[string]func(*Message){
		"To list":              func(m *Message) { m.To = "invalid, ada@example.com" },
		"address injection":    func(m *Message) { m.To += "\r\nBcc: hidden@example.com" },
		"Cc":                   func(m *Message) { m.Cc = []string{"bad"} },
		"Bcc":                  func(m *Message) { m.Bcc = []string{"bad"} },
		"From":                 func(m *Message) { m.From = "bad" },
		"ReplyTo":              func(m *Message) { m.ReplyTo = "reply@example.com\nX-Secret: yes" },
		"count":                func(m *Message) { m.Cc = append(m.Cc, "extra@example.com") },
		"duplicate":            func(m *Message) { m.Bcc = []string{"ada@example.com"} },
		"subject":              func(m *Message) { m.Subject += "\r\nBcc: hidden@example.com" },
		"body encoding":        func(m *Message) { m.HTML = string([]byte{255}) },
		"header name":          func(m *Message) { m.Headers = map[string]string{"X-Foo: bad": "yes"} },
		"header value":         func(m *Message) { m.Headers = map[string]string{"X-Foo": "yes\nBcc: hidden@example.com"} },
		"reserved header":      func(m *Message) { m.Headers = map[string]string{"bCc": "hidden@example.com"} },
		"message id header":    func(m *Message) { m.Headers = map[string]string{"Message-ID": "<own@example.com>"} },
		"date header":          func(m *Message) { m.Headers = map[string]string{"Date": "Mon, 1 Jan 2024 00:00:00 +0000"} },
		"file count":           func(m *Message) { m.Attachments = append(m.Attachments, Attachment{Name: "a.txt"}) },
		"file bytes":           func(m *Message) { m.Attachments[0].Data = make([]byte, 1024) },
		"file name":            func(m *Message) { m.Attachments[0].Name = "../report.txt" },
		"file name injection":  func(m *Message) { m.Attachments[0].Name = "report\n.txt" },
		"file type":            func(m *Message) { m.Attachments[0].ContentType = "invalid" },
		"multipart attachment": func(m *Message) { m.Attachments[0].ContentType = "multipart/mixed" },
		"file type injection":  func(m *Message) { m.Attachments[0].ContentType = "text/plain\r\nX: yes" },
	} {
		t.Run(name, func(t *testing.T) {
			msg := attachedMessage()
			change(&msg)
			if _, err := m.Send(context.Background(), msg); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
	original := attachedMessage()
	copy := original
	if err := m.prepare(context.Background(), &copy); err != nil {
		t.Fatal(err)
	}
	if original.Attachments[0].ContentType != "" {
		t.Fatal("caller metadata changed")
	}
	empty := Message{To: "ada@example.com", Subject: "Hi", Text: "hello", Attachments: []Attachment{{Name: "empty.txt"}}}
	if err := m.prepare(context.Background(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Attachments[0].Data == nil {
		t.Fatal("empty data would encode as null")
	}
	for _, name := range []string{"sendgrid", "resend", "postmark"} {
		t.Run(name+" empty file", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				key := `"content":""`
				if name == "postmark" {
					key = `"Content":""`
				}
				if !bytes.Contains(data, []byte(key)) {
					t.Errorf("empty content is not a base64 string: %s", data)
				}
				io.WriteString(w, `{"id":"sent","MessageID":"sent"}`)
			}))
			defer server.Close()
			sender, err := New(Config{Provider: name, APIKey: "test", BaseURL: server.URL, From: "sender@example.com"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sender.Send(ctxWith(t, server), empty); err != nil {
				t.Fatal(err)
			}
		})
	}
	defaults, err := New(Config{Provider: "outbox"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.cfg.MaxRecipients != 50 || defaults.cfg.MaxAttachments != 10 || defaults.cfg.MaxAttachmentBytes != 10<<20 {
		t.Fatal(defaults.cfg)
	}
}

func TestProvidersRecipientsAndAttachments(t *testing.T) {
	for _, provider := range []string{"mailgun", "sendgrid", "postmark", "resend"} {
		t.Run(provider, func(t *testing.T) {
			requests := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if provider == "mailgun" {
					if err := r.ParseMultipartForm(4096); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					for key, count := range map[string]int{"to": 2, "cc": 1, "bcc": 1} {
						if len(r.MultipartForm.Value[key]) != count {
							t.Errorf("%s: %v", key, r.MultipartForm.Value)
						}
					}
					files := r.MultipartForm.File["attachment"]
					if len(files) != 2 {
						t.Errorf("files: %v", files)
					}
					for i, file := range files {
						f, err := file.Open()
						if err != nil {
							t.Error(err)
							continue
						}
						data, err := io.ReadAll(f)
						f.Close()
						want := attachedMessage().Attachments[i]
						if err != nil || file.Filename != want.Name || !bytes.Equal(data, want.Data) {
							t.Error("multipart attachment corrupted")
						}
					}
					requests <- nil
				} else {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					requests <- data
				}
				w.Header().Set("X-Message-Id", "sent")
				io.WriteString(w, `{"id":"sent","MessageID":"sent"}`)
			}))
			defer server.Close()
			m, err := New(Config{Provider: provider, APIKey: "test", Domain: "example.com", BaseURL: server.URL}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Send(ctxWith(t, server), attachedMessage()); err != nil {
				t.Fatal(err)
			}
			data := <-requests
			if provider == "mailgun" {
				return
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			if provider == "postmark" {
				for key, want := range map[string]string{"To": attachedMessage().To, "Cc": "cc@example.com", "Bcc": "blind@example.com"} {
					var got string
					json.Unmarshal(payload[key], &got)
					if got != want {
						t.Errorf("%s: %q", key, got)
					}
				}
			} else {
				recipients := payload
				if provider == "sendgrid" {
					var personalizations []map[string]json.RawMessage
					if err := json.Unmarshal(payload["personalizations"], &personalizations); err != nil || len(personalizations) != 1 {
						t.Fatal(err, string(data))
					}
					recipients = personalizations[0]
				}
				for key, want := range map[string][]string{"to": {"ada@example.com", "lin@example.com"}, "cc": {"cc@example.com"}, "bcc": {"blind@example.com"}} {
					var addresses []string
					if provider == "sendgrid" {
						var values []struct{ Email, Name string }
						if err := json.Unmarshal(recipients[key], &values); err != nil {
							t.Fatal(err)
						}
						for _, v := range values {
							addresses = append(addresses, v.Email)
						}
					} else {
						var values []string
						if err := json.Unmarshal(recipients[key], &values); err != nil {
							t.Fatal(err)
						}
						for _, v := range values {
							a, err := mail.ParseAddress(v)
							if err != nil {
								t.Fatal(err)
							}
							addresses = append(addresses, a.Address)
						}
					}
					if !reflect.DeepEqual(addresses, want) {
						t.Errorf("%s: %v", key, addresses)
					}
				}
			}
			key, nameKey, typeKey, contentKey := "attachments", "filename", "content_type", "content"
			if provider == "sendgrid" {
				typeKey = "type"
			}
			if provider == "postmark" {
				key, nameKey, typeKey, contentKey = "Attachments", "Name", "ContentType", "Content"
			}
			var files []map[string]string
			if err := json.Unmarshal(payload[key], &files); err != nil || len(files) != 2 {
				t.Fatal(err, string(data))
			}
			for i, file := range files {
				want := attachedMessage().Attachments[i]
				decoded, err := base64.StdEncoding.DecodeString(file[contentKey])
				if err != nil || !bytes.Equal(decoded, want.Data) || file[nameKey] != want.Name {
					t.Error("JSON attachment corrupted", file)
				}
				if file[typeKey] == "" || (provider == "sendgrid" && strings.Contains(file[typeKey], ";")) {
					t.Error("invalid attachment type", file)
				}
			}
		})
	}
}

func TestMIMEAttachmentsAndBlindRecipients(t *testing.T) {
	msg := attachedMessage()
	m, _ := New(Config{Provider: "outbox"}, nil, nil)
	if err := m.prepare(context.Background(), &msg); err != nil {
		t.Fatal(err)
	}
	from, _ := mail.ParseAddress(msg.From)
	first, _ := mail.ParseAddress("a@b.c") // The ID must not slice short addresses.
	raw, id, err := buildMIME(msg, from, first)
	if err != nil || id == "" {
		t.Fatal(err, id)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("Bcc") != "" || bytes.Contains(raw, []byte("blind@example.com")) {
		t.Fatal("Bcc disclosed in MIME")
	}
	for key, want := range map[string]int{"To": 2, "Cc": 1} {
		list, err := parsed.Header.AddressList(key)
		if err != nil || len(list) != want {
			t.Fatal(key, list, err)
		}
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != msg.Subject {
		t.Fatal(subject, err)
	}
	kind, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || kind != "multipart/mixed" {
		t.Fatal(kind, err)
	}
	mixed := multipart.NewReader(parsed.Body, params["boundary"])
	body, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	kind, params, err = mime.ParseMediaType(body.Header.Get("Content-Type"))
	if err != nil || kind != "multipart/alternative" {
		t.Fatal(kind, err)
	}
	alternatives := multipart.NewReader(body, params["boundary"])
	for _, want := range []string{msg.Text, msg.HTML} {
		part, err := alternatives.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil || string(data) != want {
			t.Fatal(string(data), err)
		}
	}
	if _, err := alternatives.NextPart(); err != io.EOF {
		t.Fatal(err)
	}
	for _, want := range msg.Attachments {
		part, err := mixed.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil || params["filename"] != want.Name {
			t.Fatal(params, err)
		}
		encoded, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(encoded), "\r\n") {
			if len(line) > 76 {
				t.Fatal("base64 line too long")
			}
		}
		data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded)))
		if err != nil || !bytes.Equal(data, want.Data) {
			t.Fatal("MIME attachment corrupted", err)
		}
	}
	if _, err := mixed.NextPart(); err != io.EOF {
		t.Fatal(err)
	}
	msg = Message{To: "a@b.c", Subject: "Hi", Text: "hello"}
	if _, _, err := buildMIME(msg, from, first); err != nil {
		t.Fatal(err)
	}
}
