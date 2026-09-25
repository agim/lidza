// Package mail is the official transactional email pack: one Send behind
// which a provider delivers (Mailgun, SendGrid, Postmark, Resend, SMTP),
// or the message is logged or kept in the outbox for development and
// tests. With the db pack every message is a row in mail_message (the
// outbox: status, provider id, error, attempts); with the jobs pack
// delivery runs in a job with retries, so a request never waits on a
// mail API. Bodies come from templates in mail/ (<name>.txt.tmpl,
// <name>.html.tmpl) or from the message itself.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/packs/jobs"
	"github.com/agim/lidza/pkg/env"
	texttemplate "text/template"
)

// Config comes from the environment.
type Config struct {
	// Provider is log (default), outbox (record only; tests), smtp,
	// mailgun, sendgrid, postmark or resend.
	Provider string `env:"MAIL_PROVIDER" default:"log"`
	// From is the default sender, "Name <address>" or an address.
	From string `env:"MAIL_FROM"`
	// APIKey authenticates the HTTP providers.
	APIKey string `env:"MAIL_API_KEY"`
	// Domain is the sending domain (Mailgun).
	Domain string `env:"MAIL_DOMAIN"`
	// BaseURL overrides a provider's API base (Mailgun EU:
	// https://api.eu.mailgun.net).
	BaseURL string `env:"MAIL_BASE_URL"`
	// SMTPURL is smtp://user:pass@host:587 (STARTTLS) or
	// smtps://user:pass@host:465 (TLS).
	SMTPURL string `env:"MAIL_SMTP_URL"`
	// TemplatesDir holds <name>.txt.tmpl and <name>.html.tmpl.
	TemplatesDir string `env:"MAIL_TEMPLATES" default:"mail"`
	// MaxAttempts bounds delivery retries through the jobs pack.
	MaxAttempts int `env:"MAIL_MAX_ATTEMPTS" default:"5"`
	// AppURL is where the app is reached from an inbox
	// (https://app.example.com); Link puts it in front of a path. Empty
	// keeps links relative, which only the outbox can follow.
	AppURL string `env:"APP_URL"`
}

// Message is what Send takes. Text and HTML are the bodies; Template
// names mail/<Template>.txt.tmpl and mail/<Template>.html.tmpl, rendered
// with Data, and fills whichever body is empty.
type Message struct {
	To       string            `json:"to"`
	Subject  string            `json:"subject"`
	Text     string            `json:"text,omitempty"`
	HTML     string            `json:"html,omitempty"`
	Template string            `json:"template,omitempty"`
	Data     any               `json:"data,omitempty"`
	From     string            `json:"from,omitempty"`
	ReplyTo  string            `json:"replyTo,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// Status values of an outbox row.
const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusFailed = "failed"
)

// JobKind is the jobs pack kind delivery runs under.
const JobKind = "mail.send"

// Mail is the running pack.
type Mail struct {
	cfg      Config
	provider Provider
	pool     *pgxpool.Pool
	queue    *jobs.Queue
	log      *slog.Logger
	mu       sync.RWMutex
	text     map[string]*texttemplate.Template
	html     map[string]*htmltemplate.Template
}

// Pack returns the pack for packs.go. List lidza/db and lidza/jobs before
// it to get the outbox and background delivery.
func Pack() lidza.Pack { return &Mail{log: slog.Default()} }

// New builds the pack outside the lifecycle (tests); pool and queue may
// be nil.
func New(cfg Config, pool *pgxpool.Pool, queue *jobs.Queue) (*Mail, error) {
	m := &Mail{cfg: cfg, pool: pool, queue: queue, log: slog.Default()}
	if err := m.setup(); err != nil {
		return nil, err
	}
	return m, nil
}

// From returns the pack from a request context.
func From(ctx context.Context) *Mail { return lidza.Service[*Mail](ctx) }

// Name implements lidza.Pack.
func (m *Mail) Name() string { return "lidza/mail" }

// Start reads the configuration, picks the provider, loads the templates
// and, when the jobs pack runs, registers the delivery handler.
func (m *Mail) Start(ctx context.Context, s *lidza.Services) error {
	if err := env.Load(".", &m.cfg); err != nil {
		return err
	}
	if pool, ok := s.Lookup(typeOf[*pgxpool.Pool]()); ok {
		m.pool = pool.(*pgxpool.Pool)
	}
	if q, ok := s.Lookup(typeOf[*jobs.Queue]()); ok {
		m.queue = q.(*jobs.Queue)
	}
	if err := m.setup(); err != nil {
		return err
	}
	lidza.Provide(s, m)
	return nil
}

func (m *Mail) setup() error {
	if m.cfg.MaxAttempts <= 0 {
		m.cfg.MaxAttempts = 5
	}
	if m.cfg.TemplatesDir == "" {
		m.cfg.TemplatesDir = "mail"
	}
	p, err := newProvider(m.cfg)
	if err != nil {
		return err
	}
	m.provider = p
	if err := m.loadTemplates(); err != nil {
		return err
	}
	if m.queue != nil && m.pool != nil {
		m.queue.Handle(JobKind, func(ctx context.Context, payload json.RawMessage) error {
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(payload, &p); err != nil {
				return err
			}
			return m.Deliver(ctx, p.ID)
		})
	}
	return nil
}

// Stop implements lidza.Pack.
func (m *Mail) Stop(context.Context) error { return nil }

// Provider names the configured provider.
func (m *Mail) Provider() string { return m.cfg.Provider }

// Link returns an absolute URL for a path of this app (APP_URL plus the
// path), for the links in a message; the path alone when APP_URL is not
// set.
func (m *Mail) Link(path string) string {
	base := strings.TrimRight(m.cfg.AppURL, "/")
	if base == "" {
		return path
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

// Send sends a message. With the outbox (db pack) it returns the row id:
// the message is queued for the jobs pack when it runs, delivered before
// returning otherwise, and the row records the outcome. Without the
// outbox the provider's message id comes back.
func (m *Mail) Send(ctx context.Context, msg Message) (string, error) {
	if err := m.prepare(&msg); err != nil {
		return "", err
	}
	if m.pool == nil {
		return m.provider.Send(ctx, msg)
	}
	var id string
	err := m.pool.QueryRow(ctx, `INSERT INTO mail_message (recipient, subject, text, html, template, status, attempts) VALUES ($1, $2, $3, $4, $5, $6, 0) RETURNING id`,
		msg.To, msg.Subject, nullable(msg.Text), nullable(msg.HTML), nullable(msg.Template), StatusQueued).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("mail: outbox: %w", err)
	}
	if m.queue != nil {
		if _, err := m.queue.Enqueue(ctx, JobKind, map[string]string{"id": id}, jobs.MaxAttempts(m.cfg.MaxAttempts)); err != nil {
			return "", err
		}
		return id, nil
	}
	return id, m.Deliver(ctx, id)
}

// Deliver sends the outbox row id through the provider and records the
// outcome. The jobs pack calls it; an error makes the job retry.
func (m *Mail) Deliver(ctx context.Context, id string) error {
	if m.pool == nil {
		return errors.New("mail: no outbox")
	}
	var msg Message
	var text, html, template *string
	err := m.pool.QueryRow(ctx, `SELECT recipient, subject, text, html, template FROM mail_message WHERE id = $1`, id).Scan(&msg.To, &msg.Subject, &text, &html, &template)
	if err != nil {
		return fmt.Errorf("mail: outbox row %s: %w", id, err)
	}
	msg.Text, msg.HTML = deref(text), deref(html)
	providerID, sendErr := m.provider.Send(ctx, msg)
	if sendErr != nil {
		m.pool.Exec(ctx, `UPDATE mail_message SET status = $2, error = $3, attempts = attempts + 1 WHERE id = $1`, id, StatusFailed, sendErr.Error())
		return sendErr
	}
	_, err = m.pool.Exec(ctx, `UPDATE mail_message SET status = $2, provider_id = $3, error = NULL, attempts = attempts + 1, sent_at = now() WHERE id = $1`, id, StatusSent, nullable(providerID))
	return err
}

// prepare validates the address, applies the default sender and renders
// the template bodies.
func (m *Mail) prepare(msg *Message) error {
	if _, err := mail.ParseAddress(msg.To); err != nil {
		return fmt.Errorf("mail: to %q: %w", msg.To, err)
	}
	if msg.Subject == "" {
		return errors.New("mail: subject is empty")
	}
	if msg.From == "" {
		msg.From = m.cfg.From
	}
	if msg.Template != "" {
		text, html, err := m.render(msg.Template, msg.Data)
		if err != nil {
			return err
		}
		if msg.Text == "" {
			msg.Text = text
		}
		if msg.HTML == "" {
			msg.HTML = html
		}
	}
	if msg.Text == "" && msg.HTML == "" {
		return errors.New("mail: no body: set Text or HTML, or a Template with mail/<name>.txt.tmpl")
	}
	return nil
}

// loadTemplates parses every <name>.txt.tmpl (text/template) and
// <name>.html.tmpl (html/template) in the templates directory. A missing
// directory means no templates.
func (m *Mail) loadTemplates() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.text, m.html = map[string]*texttemplate.Template{}, map[string]*htmltemplate.Template{}
	entries, err := os.ReadDir(m.cfg.TemplatesDir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		p := filepath.Join(m.cfg.TemplatesDir, name)
		switch {
		case strings.HasSuffix(name, ".txt.tmpl"):
			t, err := texttemplate.ParseFiles(p)
			if err != nil {
				return fmt.Errorf("mail template %s: %w", p, err)
			}
			m.text[strings.TrimSuffix(name, ".txt.tmpl")] = t
		case strings.HasSuffix(name, ".html.tmpl"):
			t, err := htmltemplate.ParseFiles(p)
			if err != nil {
				return fmt.Errorf("mail template %s: %w", p, err)
			}
			m.html[strings.TrimSuffix(name, ".html.tmpl")] = t
		}
	}
	return nil
}

// Templates lists the template names found.
func (m *Mail) Templates() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for n := range m.text {
		seen[n] = true
	}
	for n := range m.html {
		seen[n] = true
	}
	for n := range seen {
		out = append(out, n)
	}
	return out
}

func (m *Mail) render(name string, data any) (text, html string, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, h := m.text[name], m.html[name]
	if t == nil && h == nil {
		return "", "", fmt.Errorf("mail: no template %q in %s (expected %s.txt.tmpl or %s.html.tmpl)", name, m.cfg.TemplatesDir, name, name)
	}
	var buf bytes.Buffer
	if t != nil {
		if err := t.Execute(&buf, data); err != nil {
			return "", "", fmt.Errorf("mail template %s: %w", name, err)
		}
		text = buf.String()
		buf.Reset()
	}
	if h != nil {
		if err := h.Execute(&buf, data); err != nil {
			return "", "", fmt.Errorf("mail template %s: %w", name, err)
		}
		html = buf.String()
	}
	return text, html, nil
}

// Stored is a row of the outbox.
type Stored struct {
	ID         string     `json:"id"`
	To         string     `json:"to"`
	Subject    string     `json:"subject"`
	Text       *string    `json:"text,omitempty"`
	HTML       *string    `json:"html,omitempty"`
	Template   *string    `json:"template,omitempty"`
	Status     string     `json:"status"`
	ProviderID *string    `json:"providerId,omitempty"`
	Error      *string    `json:"error,omitempty"`
	Attempts   int        `json:"attempts"`
	CreatedAt  time.Time  `json:"createdAt"`
	SentAt     *time.Time `json:"sentAt,omitempty"`
}

// Outbox returns the newest messages of the outbox; tests read the
// verification link from here, agents through the MCP tool lidza_mail.
func (m *Mail) Outbox(ctx context.Context, limit int) ([]Stored, error) {
	if m.pool == nil {
		return nil, errors.New("mail: no outbox (the db pack is not enabled)")
	}
	return Recent(ctx, m.pool, limit)
}

// WaitFor polls the outbox for the newest message to an address whose
// subject contains subject, for tests of mail sent by a job (it lands a
// moment after the request that caused it); a message sent in the
// request itself is found at once.
func (m *Mail) WaitFor(ctx context.Context, to, subject string, timeout time.Duration) (Stored, error) {
	deadline := time.Now().Add(timeout)
	for {
		rows, err := m.Outbox(ctx, 50)
		if err != nil {
			return Stored{}, err
		}
		for _, row := range rows {
			if strings.EqualFold(row.To, to) && strings.Contains(row.Subject, subject) {
				return row, nil
			}
		}
		if time.Now().After(deadline) {
			return Stored{}, fmt.Errorf("mail: no message to %s with subject %q within %s", to, subject, timeout)
		}
		select {
		case <-ctx.Done():
			return Stored{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Recent reads the outbox with a pool, newest first.
func Recent(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Stored, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := pool.Query(ctx, `SELECT id, recipient, subject, text, html, template, status, provider_id, error, attempts, created_at, sent_at FROM mail_message ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var s Stored
		if err := rows.Scan(&s.ID, &s.To, &s.Subject, &s.Text, &s.HTML, &s.Template, &s.Status, &s.ProviderID, &s.Error, &s.Attempts, &s.CreatedAt, &s.SentAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// OutboxTable is the DDL of the outbox (model MailMessage, table
// mail_message in the schema fragment). Tests create it directly.
const OutboxTable = `CREATE TABLE IF NOT EXISTS mail_message (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  recipient text NOT NULL,
  subject text NOT NULL,
  text text,
  html text,
  template text,
  status text NOT NULL,
  provider_id text,
  error text,
  attempts integer NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  sent_at timestamptz
);
CREATE INDEX IF NOT EXISTS mail_message_recipient_idx ON mail_message (recipient);
CREATE INDEX IF NOT EXISTS mail_message_status_idx ON mail_message (status);
CREATE INDEX IF NOT EXISTS mail_message_created_at_idx ON mail_message (created_at);`
