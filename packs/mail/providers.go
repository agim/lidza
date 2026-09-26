package mail

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/agim/lidza"
)

// Provider delivers one message and returns the provider's id for it.
type Provider interface {
	Send(ctx context.Context, msg Message) (string, error)
}

// Providers lists the provider names.
var Providers = []string{"log", "outbox", "smtp", "mailgun", "sendgrid", "postmark", "resend"}

func newProvider(cfg Config) (Provider, error) {
	need := func(what, value string) error {
		if value == "" {
			return fmt.Errorf("mail: provider %s needs %s", cfg.Provider, what)
		}
		return nil
	}
	switch cfg.Provider {
	case "", "log":
		return logProvider{}, nil
	case "outbox":
		return outboxProvider{}, nil
	case "smtp":
		if cfg.SMTPURL != "" {
			return newSMTP(cfg.SMTPURL)
		}
		if err := need("MAIL_SMTP_HOST (or MAIL_SMTP_URL)", cfg.SMTPHost); err != nil {
			return nil, err
		}
		return smtpFromParts(cfg)
	case "mailgun":
		if err := errors.Join(need("MAIL_API_KEY", cfg.APIKey), need("MAIL_DOMAIN", cfg.Domain)); err != nil {
			return nil, err
		}
		return mailgun{key: cfg.APIKey, domain: cfg.Domain, base: or(cfg.BaseURL, regional(cfg.Region, "https://api.mailgun.net", "https://api.eu.mailgun.net"))}, nil
	case "sendgrid":
		if err := need("MAIL_API_KEY", cfg.APIKey); err != nil {
			return nil, err
		}
		return sendgrid{key: cfg.APIKey, base: or(cfg.BaseURL, regional(cfg.Region, "https://api.sendgrid.com", "https://api.eu.sendgrid.com"))}, nil
	case "postmark":
		if err := need("MAIL_API_KEY", cfg.APIKey); err != nil {
			return nil, err
		}
		return postmark{key: cfg.APIKey, base: or(cfg.BaseURL, "https://api.postmarkapp.com")}, nil
	case "resend":
		if err := need("MAIL_API_KEY", cfg.APIKey); err != nil {
			return nil, err
		}
		return resend{key: cfg.APIKey, base: or(cfg.BaseURL, "https://api.resend.com")}, nil
	}
	return nil, fmt.Errorf("mail: unknown provider %q; one of %s", cfg.Provider, strings.Join(Providers, ", "))
}

// Regions a provider's API may be in (MAIL_REGION).
var Regions = []string{"us", "eu"}

// regional picks the EU base for region "eu", the default one otherwise.
func regional(region, us, eu string) string {
	if strings.EqualFold(region, "eu") {
		return eu
	}
	return us
}

// SMTPSecurities are the values of MAIL_SMTP_SECURITY.
var SMTPSecurities = []string{"starttls", "tls", "none"}

// smtpFromParts builds the SMTP provider from the separate settings.
func smtpFromParts(cfg Config) (Provider, error) {
	security := strings.ToLower(or(cfg.SMTPSecurity, "starttls"))
	p := smtpProvider{host: cfg.SMTPHost, user: cfg.SMTPUsername, pass: cfg.SMTPPassword}
	port := map[string]string{"starttls": "587", "tls": "465", "none": "25"}
	switch security {
	case "starttls":
		p.requireTLS = true
	case "tls":
		p.implicitTLS = true
	case "none":
		p.plain = true
	default:
		return nil, fmt.Errorf("mail: MAIL_SMTP_SECURITY is %q; one of %s", cfg.SMTPSecurity, strings.Join(SMTPSecurities, ", "))
	}
	p.port = port[security]
	if cfg.SMTPPort > 0 {
		p.port = strconv.Itoa(cfg.SMTPPort)
	}
	return p, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// logProvider writes the message to the log; the default outside tests.
type logProvider struct{}

func (logProvider) Send(ctx context.Context, msg Message) (string, error) {
	lidza.Log(ctx).Info("mail (MAIL_PROVIDER=log, not sent)", "to", msg.To, "from", msg.From, "subject", msg.Subject, "text", msg.Text)
	return "", nil
}

// outboxProvider sends nothing: the outbox row is the record. Tests use it.
type outboxProvider struct{}

func (outboxProvider) Send(context.Context, Message) (string, error) { return "", nil }

// post sends an HTTP request through the app's client (recordable in
// tests) and returns the body; a status outside 2xx is an error carrying
// the provider's text.
func post(ctx context.Context, req *http.Request) ([]byte, http.Header, error) {
	res, err := lidza.HTTPClient(ctx).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, nil, fmt.Errorf("%s: %d %s", req.URL.Host, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, res.Header, nil
}

func jsonRequest(ctx context.Context, url string, v any) (*http.Request, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(buf.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// mailgun: POST /v3/<domain>/messages, form fields, basic auth "api:<key>".
type mailgun struct{ key, domain, base string }

func (p mailgun) Send(ctx context.Context, msg Message) (string, error) {
	form := url.Values{"from": {msg.From}, "to": {msg.To}, "subject": {msg.Subject}}
	if msg.Text != "" {
		form.Set("text", msg.Text)
	}
	if msg.HTML != "" {
		form.Set("html", msg.HTML)
	}
	if msg.ReplyTo != "" {
		form.Set("h:Reply-To", msg.ReplyTo)
	}
	for k, v := range msg.Headers {
		form.Set("h:"+k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/v3/"+p.domain+"/messages", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("api", p.key)
	body, _, err := post(ctx, req)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &out)
	return out.ID, nil
}

// sendgrid: POST /v3/mail/send, JSON, bearer key; the id is a header.
type sendgrid struct{ key, base string }

func (p sendgrid) Send(ctx context.Context, msg Message) (string, error) {
	from, err := parseAddress(msg.From)
	if err != nil {
		return "", err
	}
	to, err := parseAddress(msg.To)
	if err != nil {
		return "", err
	}
	var content []map[string]string
	if msg.Text != "" {
		content = append(content, map[string]string{"type": "text/plain", "value": msg.Text})
	}
	if msg.HTML != "" {
		content = append(content, map[string]string{"type": "text/html", "value": msg.HTML})
	}
	payload := map[string]any{
		"personalizations": []map[string]any{{"to": []map[string]string{{"email": to.Address, "name": to.Name}}}},
		"from":             map[string]string{"email": from.Address, "name": from.Name},
		"subject":          msg.Subject,
		"content":          content,
	}
	if msg.ReplyTo != "" {
		reply, err := parseAddress(msg.ReplyTo)
		if err != nil {
			return "", err
		}
		payload["reply_to"] = map[string]string{"email": reply.Address, "name": reply.Name}
	}
	if len(msg.Headers) > 0 {
		payload["headers"] = msg.Headers
	}
	req, err := jsonRequest(ctx, p.base+"/v3/mail/send", payload)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	_, headers, err := post(ctx, req)
	if err != nil {
		return "", err
	}
	return headers.Get("X-Message-Id"), nil
}

// postmark: POST /email, JSON, server token header.
type postmark struct{ key, base string }

func (p postmark) Send(ctx context.Context, msg Message) (string, error) {
	payload := map[string]any{"From": msg.From, "To": msg.To, "Subject": msg.Subject, "MessageStream": "outbound"}
	if msg.Text != "" {
		payload["TextBody"] = msg.Text
	}
	if msg.HTML != "" {
		payload["HtmlBody"] = msg.HTML
	}
	if msg.ReplyTo != "" {
		payload["ReplyTo"] = msg.ReplyTo
	}
	if len(msg.Headers) > 0 {
		var hs []map[string]string
		for k, v := range msg.Headers {
			hs = append(hs, map[string]string{"Name": k, "Value": v})
		}
		payload["Headers"] = hs
	}
	req, err := jsonRequest(ctx, p.base+"/email", payload)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Postmark-Server-Token", p.key)
	body, _, err := post(ctx, req)
	if err != nil {
		return "", err
	}
	var out struct {
		MessageID string `json:"MessageID"`
	}
	_ = json.Unmarshal(body, &out)
	return out.MessageID, nil
}

// resend: POST /emails, JSON, bearer key.
type resend struct{ key, base string }

func (p resend) Send(ctx context.Context, msg Message) (string, error) {
	payload := map[string]any{"from": msg.From, "to": []string{msg.To}, "subject": msg.Subject}
	if msg.Text != "" {
		payload["text"] = msg.Text
	}
	if msg.HTML != "" {
		payload["html"] = msg.HTML
	}
	if msg.ReplyTo != "" {
		payload["reply_to"] = msg.ReplyTo
	}
	if len(msg.Headers) > 0 {
		payload["headers"] = msg.Headers
	}
	req, err := jsonRequest(ctx, p.base+"/emails", payload)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	body, _, err := post(ctx, req)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &out)
	return out.ID, nil
}

// smtpProvider speaks SMTP with PLAIN or LOGIN auth: STARTTLS on smtp://,
// implicit TLS on smtps://.
type smtpProvider struct {
	host, port, user, pass string
	implicitTLS            bool
	// requireTLS refuses a server without STARTTLS; plain never
	// upgrades. A URL (smtp://) upgrades when the server offers it.
	requireTLS, plain bool
}

func newSMTP(raw string) (Provider, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "smtp" && u.Scheme != "smtps") {
		return nil, fmt.Errorf("mail: MAIL_SMTP_URL must be smtp://user:pass@host:587 or smtps://user:pass@host:465")
	}
	p := smtpProvider{host: u.Hostname(), port: u.Port(), implicitTLS: u.Scheme == "smtps"}
	if p.port == "" {
		p.port = map[bool]string{true: "465", false: "587"}[p.implicitTLS]
	}
	if u.User != nil {
		p.user = u.User.Username()
		p.pass, _ = u.User.Password()
	}
	return p, nil
}

func (p smtpProvider) Send(ctx context.Context, msg Message) (string, error) {
	from, err := parseAddress(msg.From)
	if err != nil {
		return "", err
	}
	to, err := parseAddress(msg.To)
	if err != nil {
		return "", err
	}
	raw, id, err := buildMIME(msg, from, to)
	if err != nil {
		return "", err
	}
	addr := net.JoinHostPort(p.host, p.port)
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var client *smtp.Client
	if p.implicitTLS {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: p.host})
		if err != nil {
			return "", err
		}
		client, err = smtp.NewClient(conn, p.host)
		if err != nil {
			return "", err
		}
	} else {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return "", err
		}
		client, err = smtp.NewClient(conn, p.host)
		if err != nil {
			return "", err
		}
		if ok, _ := client.Extension("STARTTLS"); ok && !p.plain {
			if err := client.StartTLS(&tls.Config{ServerName: p.host}); err != nil {
				return "", err
			}
		} else if p.requireTLS {
			client.Close()
			return "", fmt.Errorf("mail: %s does not offer STARTTLS; set MAIL_SMTP_SECURITY=tls for port 465, or none for a local relay", addr)
		}
	}
	defer client.Close()
	if p.user != "" {
		if err := client.Auth(smtp.PlainAuth("", p.user, p.pass, p.host)); err != nil {
			return "", err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return "", err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return "", err
	}
	w, err := client.Data()
	if err != nil {
		return "", err
	}
	if _, err := w.Write(raw); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return id, client.Quit()
}

// buildMIME renders the message as RFC 5322 with a multipart/alternative
// body when both parts are present. It returns the Message-ID it set.
func buildMIME(msg Message, from, to *mail.Address) ([]byte, string, error) {
	id := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), base64.RawURLEncoding.EncodeToString([]byte(to.Address))[:8], domainOf(from.Address))
	var b strings.Builder
	h := textproto.MIMEHeader{}
	h.Set("From", from.String())
	h.Set("To", to.String())
	h.Set("Subject", msg.Subject)
	h.Set("Date", time.Now().Format(time.RFC1123Z))
	h.Set("Message-ID", id)
	h.Set("MIME-Version", "1.0")
	if msg.ReplyTo != "" {
		h.Set("Reply-To", msg.ReplyTo)
	}
	for k, v := range msg.Headers {
		h.Set(k, v)
	}
	writeHeaders := func(h textproto.MIMEHeader) {
		for k, vs := range h {
			for _, v := range vs {
				fmt.Fprintf(&b, "%s: %s\r\n", k, v)
			}
		}
	}
	switch {
	case msg.Text != "" && msg.HTML != "":
		mw := multipart.NewWriter(&b)
		h.Set("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
		writeHeaders(h)
		b.WriteString("\r\n")
		for _, part := range []struct{ ctype, body string }{{"text/plain; charset=utf-8", msg.Text}, {"text/html; charset=utf-8", msg.HTML}} {
			pw, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {part.ctype}, "Content-Transfer-Encoding": {"quoted-printable"}})
			if err != nil {
				return nil, "", err
			}
			writeQuotedPrintable(pw, part.body)
		}
		mw.Close()
	case msg.HTML != "":
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		writeHeaders(h)
		b.WriteString("\r\n")
		writeQuotedPrintable(&b, msg.HTML)
	default:
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		writeHeaders(h)
		b.WriteString("\r\n")
		writeQuotedPrintable(&b, msg.Text)
	}
	return []byte(b.String()), id, nil
}

func writeQuotedPrintable(w io.Writer, s string) {
	qp := newQPWriter(w)
	qp.Write([]byte(s))
	qp.Close()
}

func parseAddress(s string) (*mail.Address, error) {
	if s == "" {
		return nil, errors.New("mail: sender is empty: set MAIL_FROM or Message.From")
	}
	a, err := mail.ParseAddress(s)
	if err != nil {
		return nil, fmt.Errorf("mail: address %q: %w", s, err)
	}
	return a, nil
}

func domainOf(address string) string {
	if i := strings.LastIndex(address, "@"); i >= 0 {
		return address[i+1:]
	}
	return "localhost"
}
