package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
			provider, err := newSMTP(cfg.SMTPURL)
			if err != nil {
				return nil, err
			}
			smtp := provider.(smtpProvider)
			smtp.timeout = cfg.SMTPTimeout
			return smtp, nil
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
	p := smtpProvider{host: cfg.SMTPHost, user: cfg.SMTPUsername, pass: cfg.SMTPPassword, timeout: cfg.SMTPTimeout}
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
	to, cc, bcc, err := recipientLists(msg)
	if err != nil {
		return "", err
	}
	for _, file := range msg.Attachments {
		if file.ContentID != "" {
			// MIME preserves independent Content-ID and filename values
			// without rewriting the caller's HTML.
			return p.sendMIME(ctx, msg, to, cc, bcc)
		}
	}
	form := url.Values{"from": {msg.From}, "to": toStrings(msg.To, to), "subject": {msg.Subject}}
	if len(cc) > 0 {
		form["cc"] = addressStrings(cc)
	}
	if len(bcc) > 0 {
		form["bcc"] = addressStrings(bcc)
	}
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
	var data io.Reader = strings.NewReader(form.Encode())
	contentType := "application/x-www-form-urlencoded"
	if len(msg.Attachments) > 0 {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		for key, values := range form {
			for _, value := range values {
				if err := writer.WriteField(key, value); err != nil {
					return "", err
				}
			}
		}
		for _, file := range msg.Attachments {
			header := textproto.MIMEHeader{
				"Content-Disposition": {mime.FormatMediaType("form-data", map[string]string{"name": "attachment", "filename": file.Name})},
				"Content-Type":        {file.ContentType},
			}
			part, err := writer.CreatePart(header)
			if err != nil {
				return "", err
			}
			if _, err := part.Write(file.Data); err != nil {
				return "", err
			}
		}
		if err := writer.Close(); err != nil {
			return "", err
		}
		contentType, data = writer.FormDataContentType(), &buffer
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/v3/"+p.domain+"/messages", data)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
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

func (p mailgun) sendMIME(ctx context.Context, msg Message, to, cc, bcc []*mail.Address) (string, error) {
	from, err := parseAddress(msg.From)
	if err != nil {
		return "", err
	}
	message, _, err := buildMIME(msg, from, to[0])
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	// Mailgun's MIME endpoint takes all envelope recipients in `to`.
	// Cc stays in the MIME header and Bcc stays out of it.
	for _, list := range [][]*mail.Address{to, cc, bcc} {
		for _, address := range list {
			if err := writer.WriteField("to", address.Address); err != nil {
				return "", err
			}
		}
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {mime.FormatMediaType("form-data", map[string]string{"name": "message", "filename": "message.eml"})},
		"Content-Type":        {"message/rfc822"},
	})
	if err != nil {
		return "", err
	}
	if _, err := part.Write(message); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/v3/"+p.domain+"/messages.mime", &buffer)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetBasicAuth("api", p.key)
	body, _, err := post(ctx, req)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// sendgrid: POST /v3/mail/send, JSON, bearer key; the id is a header.
type sendgrid struct{ key, base string }

func (p sendgrid) Send(ctx context.Context, msg Message) (string, error) {
	from, err := parseAddress(msg.From)
	if err != nil {
		return "", err
	}
	to, cc, bcc, err := recipientLists(msg)
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
	addresses := func(list []*mail.Address) []map[string]string {
		out := make([]map[string]string, len(list))
		for i, a := range list {
			out[i] = map[string]string{"email": a.Address, "name": a.Name}
		}
		return out
	}
	personalization := map[string]any{"to": addresses(to)}
	if len(cc) > 0 {
		personalization["cc"] = addresses(cc)
	}
	if len(bcc) > 0 {
		personalization["bcc"] = addresses(bcc)
	}
	payload := map[string]any{
		"personalizations": []map[string]any{personalization},
		"from":             map[string]string{"email": from.Address, "name": from.Name},
		"subject":          msg.Subject,
		"content":          content,
	}
	if msg.ReplyTo != "" {
		reply, err := replyAddresses(msg.ReplyTo)
		if err != nil {
			return "", err
		}
		if len(reply) == 1 {
			payload["reply_to"] = addresses(reply)[0]
		} else {
			payload["reply_to_list"] = addresses(reply)
		}
	}
	if len(msg.Headers) > 0 {
		payload["headers"] = msg.Headers
	}
	if len(msg.Attachments) > 0 {
		var files []map[string]any
		for _, file := range msg.Attachments {
			// SendGrid accepts a media type without parameters.
			kind, _, err := mime.ParseMediaType(file.ContentType)
			if err != nil {
				return "", err
			}
			entry := map[string]any{"filename": file.Name, "type": kind, "content": file.Data, "disposition": "attachment"}
			if file.ContentID != "" {
				entry["disposition"], entry["content_id"] = "inline", file.ContentID
			}
			files = append(files, entry)
		}
		payload["attachments"] = files
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
	if len(msg.Cc) > 0 {
		payload["Cc"] = strings.Join(msg.Cc, ", ")
	}
	if len(msg.Bcc) > 0 {
		payload["Bcc"] = strings.Join(msg.Bcc, ", ")
	}
	if len(msg.Attachments) > 0 {
		var files []map[string]any
		for _, file := range msg.Attachments {
			entry := map[string]any{"Name": file.Name, "ContentType": file.ContentType, "Content": file.Data}
			if file.ContentID != "" {
				entry["ContentID"] = "cid:" + file.ContentID
			}
			files = append(files, entry)
		}
		payload["Attachments"] = files
	}
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
	to, cc, bcc, err := recipientLists(msg)
	if err != nil {
		return "", err
	}
	payload := map[string]any{"from": msg.From, "to": toStrings(msg.To, to), "subject": msg.Subject}
	if len(cc) > 0 {
		payload["cc"] = addressStrings(cc)
	}
	if len(bcc) > 0 {
		payload["bcc"] = addressStrings(bcc)
	}
	if len(msg.Attachments) > 0 {
		var files []map[string]any
		for _, file := range msg.Attachments {
			entry := map[string]any{"filename": file.Name, "content": file.Data, "content_type": file.ContentType}
			if file.ContentID != "" {
				entry["content_id"] = file.ContentID
			}
			files = append(files, entry)
		}
		payload["attachments"] = files
	}
	if msg.Text != "" {
		payload["text"] = msg.Text
	}
	if msg.HTML != "" {
		payload["html"] = msg.HTML
	}
	if msg.ReplyTo != "" {
		reply, err := replyAddresses(msg.ReplyTo)
		if err != nil {
			return "", err
		}
		if len(reply) == 1 {
			payload["reply_to"] = msg.ReplyTo
		} else {
			payload["reply_to"] = addressStrings(reply)
		}
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

// smtpProvider speaks SMTP with PLAIN auth: STARTTLS on smtp://,
// implicit TLS on smtps://.
type smtpProvider struct {
	host, port, user, pass string
	timeout                time.Duration
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
	to, cc, bcc, err := recipientLists(msg)
	if err != nil {
		return "", err
	}
	raw, id, err := buildMIME(msg, from, to[0])
	if err != nil {
		return "", err
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(call, "tcp", net.JoinHostPort(p.host, p.port))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	deadline, _ := call.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	original := conn
	stop := context.AfterFunc(call, func() { _ = original.Close() })
	defer stop()
	if p.implicitTLS {
		secure := tls.Client(conn, &tls.Config{ServerName: p.host})
		if err := secure.HandshakeContext(call); err != nil {
			return "", err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, p.host)
	if err != nil {
		return "", err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok && !p.plain && !p.implicitTLS {
		if err := client.StartTLS(&tls.Config{ServerName: p.host}); err != nil {
			return "", err
		}
	} else if p.requireTLS && !p.implicitTLS {
		return "", fmt.Errorf("mail: %s does not offer STARTTLS; set MAIL_SMTP_SECURITY=tls for port 465, or none for a local relay", net.JoinHostPort(p.host, p.port))
	}
	if p.user != "" {
		if err := client.Auth(smtp.PlainAuth("", p.user, p.pass, p.host)); err != nil {
			return "", err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, recipient := range append(append(to, cc...), bcc...) {
		// Preserve visible headers but send each envelope recipient once.
		if seen[recipient.Address] {
			continue
		}
		seen[recipient.Address] = true
		if err := client.Rcpt(recipient.Address); err != nil {
			return "", err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(raw); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	// The server accepted the message when DATA closed: a failed QUIT (a
	// dropped connection, a timeout) must not mark it failed, or the retry
	// sends the recipient a second copy.
	_ = client.Quit()
	return id, nil
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
