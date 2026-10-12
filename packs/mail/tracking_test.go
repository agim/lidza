package mail

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestTrackingOff: by default Mailgun, SendGrid and Postmark are told on
// every message not to track clicks or opens, so a verification link is
// never rewritten; MAIL_TRACKING leaves it to the account (#57).
func TestTrackingOff(t *testing.T) {
	var body []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{"id":"x","MessageID":"x"}`))
	}))
	defer srv.Close()
	msg := Message{From: "app@example.com", To: "ann@example.com", Subject: "Verify", Text: "https://app.example.com/verify?token=t", HTML: `<a href="https://app.example.com/verify?token=t">Verify</a>`}
	mailgunFields := func() url.Values {
		mt, params, _ := mime.ParseMediaType(contentType)
		if mt == "application/x-www-form-urlencoded" {
			v, _ := url.ParseQuery(string(body))
			return v
		}
		out := url.Values{}
		r := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
		for {
			p, err := r.NextPart()
			if err != nil {
				break
			}
			if p.FileName() == "" {
				b, _ := io.ReadAll(p)
				out.Add(p.FormName(), string(b))
			}
		}
		return out
	}
	ctx := context.Background()
	for _, tracking := range []bool{false, true} {
		cfg := Config{APIKey: "k", Domain: "mg.example.com", BaseURL: srv.URL, Tracking: tracking}

		cfg.Provider = "mailgun"
		p, _ := newProvider(cfg)
		inline := msg
		inline.Attachments = []Attachment{{Name: "logo.png", ContentType: "image/png", Data: []byte("png"), ContentID: "logo"}}
		for _, m := range []Message{msg, inline} { // the form and the MIME endpoint
			if _, err := p.Send(ctx, m); err != nil {
				t.Fatal(err)
			}
			f := mailgunFields()
			if got := f.Get("o:tracking-clicks") == "no" && f.Get("o:tracking-opens") == "no" && f.Get("o:tracking") == "no"; got == tracking {
				t.Errorf("mailgun (tracking %v, %d attachments): %v", tracking, len(m.Attachments), f)
			}
		}

		cfg.Provider = "sendgrid"
		p, _ = newProvider(cfg)
		if _, err := p.Send(ctx, msg); err != nil {
			t.Fatal(err)
		}
		var sg struct {
			Tracking *struct {
				Click struct{ Enable bool } `json:"click_tracking"`
				Open  struct{ Enable bool } `json:"open_tracking"`
			} `json:"tracking_settings"`
		}
		json.Unmarshal(body, &sg)
		if off := sg.Tracking != nil && !sg.Tracking.Click.Enable && !sg.Tracking.Open.Enable; off == tracking {
			t.Errorf("sendgrid (tracking %v): %s", tracking, body)
		}

		cfg.Provider = "postmark"
		p, _ = newProvider(cfg)
		if _, err := p.Send(ctx, msg); err != nil {
			t.Fatal(err)
		}
		var pm map[string]any
		json.Unmarshal(body, &pm)
		if off := pm["TrackLinks"] == "None" && pm["TrackOpens"] == false; off == tracking {
			t.Errorf("postmark (tracking %v): %s", tracking, body)
		}
	}
}
