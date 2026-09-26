// Package llm is the language-model pack: one Chat, Stream, Embed,
// Generate and Run over Anthropic, OpenAI, Google and Ollama, spoken
// directly over HTTP, plus a fake provider for tests. Handlers and jobs
// call llm.From(ctx); the provider and model come from .env
// (LLM_PROVIDER, LLM_MODEL, LLM_API_KEY). Every call has a deadline,
// retries on 429 and 5xx with backoff, and counts tokens on /metrics.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/env"
	"github.com/agim/lidza/pkg/jsonschema"
	"github.com/agim/lidza/pkg/validate"
)

// Config is read from .env at start.
type Config struct {
	// Provider is fake (default; scripted replies for tests and a first
	// run), ollama (a local model), anthropic, openai, google (Gemini) or
	// compatible (any server speaking the OpenAI API: llama.cpp's
	// llama-server, vLLM, LM Studio; LLM_BASE_URL required, the key
	// optional).
	Provider string `env:"LLM_PROVIDER" default:"fake"`
	// Model is the chat model; each provider has a default.
	Model string `env:"LLM_MODEL"`
	// EmbedModel is the embedding model; each provider has a default.
	EmbedModel string `env:"LLM_EMBED_MODEL"`
	// APIKey authenticates anthropic, openai and google.
	APIKey string `env:"LLM_API_KEY"`
	// BaseURL overrides the provider's API base (a proxy, a region,
	// Ollama on another host: http://127.0.0.1:11434 by default).
	BaseURL string `env:"LLM_BASE_URL"`
	// MaxTokens bounds a reply unless the request says otherwise.
	MaxTokens int `env:"LLM_MAX_TOKENS" default:"1024"`
	// Timeout bounds one attempt of one call.
	Timeout time.Duration `env:"LLM_TIMEOUT" default:"60s"`
	// MaxAttempts is how many times a call is tried on 429, 5xx or a
	// network error, with backoff (1s, 2s, 4s) and Retry-After honoured.
	MaxAttempts int `env:"LLM_MAX_ATTEMPTS" default:"3"`
	// MaxToolRounds bounds Run: how many times the model may call tools
	// before it must answer.
	MaxToolRounds int `env:"LLM_MAX_TOOL_ROUNDS" default:"8"`
}

// Role of a message.
type Role string

// Roles. The system prompt is Request.System, not a message.
const (
	User      Role = "user"
	Assistant Role = "assistant"
	// ToolResult carries a tool's output back to the model: ToolCallID
	// names the call, Name the tool, Content the result.
	ToolResult Role = "tool"
)

// Message is one turn of a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls are the calls an assistant message made.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID and Name identify the call a ToolResult answers.
	ToolCallID string `json:"toolCallId,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ToolCall is the model asking for a tool to run.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Tool is a function offered to the model: ToolsOf builds them from the
// app's lidza.Tool values.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// Request is one call to the model.
type Request struct {
	// System is the system prompt.
	System string `json:"system,omitempty"`
	// Messages is the conversation so far, ending with the user's turn or
	// tool results.
	Messages []Message `json:"messages"`
	// Tools the model may call; the reply's ToolCalls carry its choices.
	Tools []Tool `json:"tools,omitempty"`
	// Schema makes the reply a JSON document of this JSON Schema (Generate
	// sets it from a Go type). The provider enforces it where it can; the
	// reply is validated on the way in.
	Schema map[string]any `json:"schema,omitempty"`
	// Model overrides LLM_MODEL; MaxTokens overrides LLM_MAX_TOKENS.
	Model     string `json:"model,omitempty"`
	MaxTokens int    `json:"maxTokens,omitempty"`
	// Temperature, when set, replaces the provider's default.
	Temperature *float64 `json:"temperature,omitempty"`
	// Label names the feature making the call ("note.tags") for the usage
	// report (llm_usage, with the db pack).
	Label string `json:"label,omitempty"`
}

// Response is the model's reply.
type Response struct {
	// Text is the reply, or the JSON document when the request had a
	// Schema.
	Text string `json:"text"`
	// ToolCalls the model wants run; empty when it answered.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// Stop says why the model stopped: "end", "tool", "length".
	Stop  string `json:"stop"`
	Model string `json:"model"`
	Usage Usage  `json:"usage"`
}

// Usage counts tokens.
type Usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Add sums usage.
func (u *Usage) Add(o Usage) { u.Input += o.Input; u.Output += o.Output }

// Error is a provider's refusal: the status, its text, and how long it
// asked to wait. Status 0 is a network error.
type Error struct {
	Provider   string
	Status     int
	Body       string
	RetryAfter time.Duration
	Err        error
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("llm: %s: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("llm: %s: %d %s", e.Provider, e.Status, e.Body)
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable reports whether trying again can help: a rate limit, a server
// error, a network error.
func (e *Error) Retryable() bool { return e.Status == 0 || e.Status == 429 || e.Status >= 500 }

// Provider speaks one API. stream is nil for a whole reply; otherwise it
// receives the text as it arrives and the Response is returned at the
// end.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req Request, stream func(string) error) (Response, error)
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// Providers lists the provider names.
var Providers = []string{"fake", "ollama", "anthropic", "openai", "google", "compatible"}

// LLM is the running pack.
type LLM struct {
	cfg      Config
	log      *slog.Logger
	provider Provider
	pool     *pgxpool.Pool
	sleep    func(context.Context, time.Duration) error
	mu       sync.RWMutex

	calls, failures, inputTokens, outputTokens atomic.Int64
}

// Pack returns the pack for packs.go.
func Pack() lidza.Pack { return &LLM{log: slog.Default()} }

// New builds the pack from a configuration (tests; Start does it from
// .env).
func New(cfg Config) (*LLM, error) {
	l := &LLM{cfg: cfg, log: slog.Default(), sleep: sleepCtx}
	l.defaults()
	p, err := newProvider(l.cfg)
	if err != nil {
		return nil, err
	}
	l.provider = p
	return l, nil
}

func (l *LLM) defaults() {
	if l.cfg.Provider == "" {
		l.cfg.Provider = "fake"
	}
	if l.cfg.MaxTokens <= 0 {
		l.cfg.MaxTokens = 1024
	}
	if l.cfg.Timeout <= 0 {
		l.cfg.Timeout = 60 * time.Second
	}
	if l.cfg.MaxAttempts <= 0 {
		l.cfg.MaxAttempts = 3
	}
	if l.cfg.MaxToolRounds <= 0 {
		l.cfg.MaxToolRounds = 8
	}
}

// From returns the pack from a handler's, job's or tool's context.
func From(ctx context.Context) *LLM { return lidza.Service[*LLM](ctx) }

// Name implements lidza.Pack.
func (l *LLM) Name() string { return "lidza/llm" }

// Start reads the configuration and provides the pack.
func (l *LLM) Start(ctx context.Context, s *lidza.Services) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	built, err := New(cfg)
	if err != nil {
		return err
	}
	l.cfg, l.provider, l.sleep = built.cfg, built.provider, built.sleep
	if pool, ok := s.Lookup(typeOf[*pgxpool.Pool]()); ok {
		l.pool = pool.(*pgxpool.Pool)
	}
	lidza.Provide(s, l)
	return nil
}

// Stop implements lidza.Pack.
func (l *LLM) Stop(context.Context) error { return nil }

// providerNow is the provider under the lock: Reconfigure may swap it.
func (l *LLM) providerNow() Provider {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.provider
}

// Reconfigure reads .env and the credentials again and switches the
// provider and model: what the admin pages call after an LLM setting is
// saved.
func (l *LLM) Reconfigure(ctx context.Context) error {
	var cfg Config
	if err := env.Load(".", &cfg); err != nil {
		return err
	}
	built, err := New(cfg)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.cfg, l.provider = built.cfg, built.provider
	l.mu.Unlock()
	l.log.Info("llm: reconfigured", "provider", l.cfg.Provider, "model", l.cfg.Model)
	return nil
}

// Provider returns the configured provider's name.
func (l *LLM) Provider() string { return l.cfg.Provider }

// Model returns the configured chat model.
func (l *LLM) Model() string { return l.cfg.Model }

// Fake returns the fake provider to script replies in a test, nil when
// another provider is configured.
func (l *LLM) Fake() *Fake {
	f, _ := l.providerNow().(*Fake)
	return f
}

// Chat sends a request and returns the whole reply.
func (l *LLM) Chat(ctx context.Context, req Request) (Response, error) {
	return l.call(ctx, req, nil)
}

// Stream sends a request and delivers the reply's text as it arrives,
// then returns the whole Response. An error from fn stops the stream.
func (l *LLM) Stream(ctx context.Context, req Request, fn func(text string) error) (Response, error) {
	return l.call(ctx, req, fn)
}

// Embed returns one vector per text, from LLM_EMBED_MODEL.
func (l *LLM) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	start := time.Now()
	var out [][]float32
	err := l.retry(ctx, func(ctx context.Context) error {
		var err error
		out, err = l.providerNow().Embed(ctx, l.cfg.EmbedModel, texts)
		return err
	})
	l.calls.Add(1)
	if err != nil {
		l.failures.Add(1)
		return nil, err
	}
	lidza.Log(ctx).Info("llm embed", "provider", l.cfg.Provider, "texts", len(texts), "ms", time.Since(start).Milliseconds())
	return out, nil
}

// Generate asks for a T: the request's Schema is T's JSON Schema, the
// reply is decoded into a T and validated when T implements
// validate.Validator. Anthropic gets it through a forced tool, the others
// through their JSON-schema output modes.
func Generate[T any](ctx context.Context, l *LLM, req Request) (T, error) {
	var out T
	req.Schema = jsonschema.Of(out)
	res, err := l.Chat(ctx, req)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(stripFences(res.Text)), &out); err != nil {
		return out, fmt.Errorf("llm: the reply is not the requested JSON: %w (%q)", err, res.Text)
	}
	// A reply that breaks the type's rules is the model's failure, not the
	// caller's: the error is not the field error itself (a handler would
	// answer 422 as if the client had sent it) but a plain one, a 500.
	if v, ok := any(out).(validate.Validator); ok {
		if err := v.Validate(); err != nil {
			return out, fmt.Errorf("llm: the reply is invalid: %v (%s)", err, res.Text)
		}
	}
	if v, ok := any(&out).(validate.Validator); ok {
		if err := v.Validate(); err != nil {
			return out, fmt.Errorf("llm: the reply is invalid: %v (%s)", err, res.Text)
		}
	}
	return out, nil
}

// Run lets the model use the app's tools: it offers them, runs every call
// the model makes (with the packs in the context, as `lidza mcp` does),
// feeds the results back, and returns the final answer. At most
// LLM_MAX_TOOL_ROUNDS rounds; the returned Usage is the sum.
func (l *LLM) Run(ctx context.Context, req Request, tools []lidza.Tool) (Response, error) {
	byName := map[string]lidza.Tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	req.Tools = ToolsOf(tools...)
	var total Usage
	for round := 0; ; round++ {
		res, err := l.Chat(ctx, req)
		if err != nil {
			return res, err
		}
		total.Add(res.Usage)
		res.Usage = total
		if len(res.ToolCalls) == 0 {
			return res, nil
		}
		if round+1 >= l.cfg.MaxToolRounds {
			return res, fmt.Errorf("llm: the model kept calling tools after %d rounds (LLM_MAX_TOOL_ROUNDS)", l.cfg.MaxToolRounds)
		}
		req.Messages = append(req.Messages, Message{Role: Assistant, Content: res.Text, ToolCalls: res.ToolCalls})
		for _, call := range res.ToolCalls {
			result := runTool(ctx, byName, call)
			req.Messages = append(req.Messages, Message{Role: ToolResult, ToolCallID: call.ID, Name: call.Name, Content: result})
		}
	}
}

func runTool(ctx context.Context, tools map[string]lidza.Tool, call ToolCall) string {
	t, ok := tools[call.Name]
	if !ok {
		return `{"error":"no tool named ` + call.Name + `"}`
	}
	out, err := t.Handler(ctx, call.Input)
	if err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(data)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return `{"error":"result is not JSON"}`
	}
	return string(data)
}

// ToolsOf describes the app's tools for the model, schemas included.
func ToolsOf(tools ...lidza.Tool) []Tool {
	var out []Tool
	for _, t := range tools {
		schema := jsonschema.Of(t.Input)
		if t.Input == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, Tool{Name: t.Name, Description: t.Description, Schema: schema})
	}
	return out
}

// call runs one chat with defaults, retries and accounting. A streamed
// call is retried only while nothing has reached the caller.
func (l *LLM) call(ctx context.Context, req Request, stream func(string) error) (Response, error) {
	if req.Model == "" {
		req.Model = l.cfg.Model
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = l.cfg.MaxTokens
	}
	start := time.Now()
	var res Response
	started := false
	var wrapped func(string) error
	if stream != nil {
		wrapped = func(s string) error {
			started = true
			return stream(s)
		}
	}
	err := l.retry(ctx, func(ctx context.Context) error {
		if started {
			return errNoRetry
		}
		var err error
		res, err = l.providerNow().Chat(ctx, req, wrapped)
		return err
	})
	l.calls.Add(1)
	l.record(ctx, req, res, time.Since(start), err)
	if err != nil {
		l.failures.Add(1)
		lidza.Log(ctx).Warn("llm chat failed", "provider", l.cfg.Provider, "model", req.Model, "label", req.Label, "error", err, "ms", time.Since(start).Milliseconds())
		return res, err
	}
	l.inputTokens.Add(int64(res.Usage.Input))
	l.outputTokens.Add(int64(res.Usage.Output))
	lidza.Log(ctx).Info("llm chat", "provider", l.cfg.Provider, "model", res.Model, "label", req.Label, "in", res.Usage.Input, "out", res.Usage.Output, "stop", res.Stop, "tools", len(res.ToolCalls), "ms", time.Since(start).Milliseconds())
	return res, nil
}

var errNoRetry = errors.New("llm: not retried")

// retry runs fn up to MaxAttempts times, each under Timeout, backing off
// 1s, 2s, 4s (or the provider's Retry-After) after a retryable Error.
func (l *LLM) retry(ctx context.Context, fn func(context.Context) error) error {
	var last error
	for attempt := 1; attempt <= l.cfg.MaxAttempts; attempt++ {
		actx, cancel := context.WithTimeout(ctx, l.cfg.Timeout)
		err := fn(actx)
		cancel()
		if err == nil {
			return nil
		}
		if errors.Is(err, errNoRetry) {
			return last
		}
		last = err
		var e *Error
		if !errors.As(err, &e) || !e.Retryable() || attempt == l.cfg.MaxAttempts || ctx.Err() != nil {
			return err
		}
		delay := time.Duration(1<<(attempt-1)) * time.Second
		if e.RetryAfter > 0 {
			delay = e.RetryAfter
		}
		l.log.Warn("llm: retrying", "provider", l.cfg.Provider, "attempt", attempt, "in", delay, "error", err)
		if err := l.sleep(ctx, delay); err != nil {
			return last
		}
	}
	return last
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// TelemetryStats reports the pack to /metrics.
func (l *LLM) TelemetryStats() map[string]float64 {
	return map[string]float64{
		"calls":         float64(l.calls.Load()),
		"failures":      float64(l.failures.Load()),
		"input_tokens":  float64(l.inputTokens.Load()),
		"output_tokens": float64(l.outputTokens.Load()),
	}
}

func newProvider(cfg Config) (Provider, error) {
	need := func() error {
		if cfg.APIKey == "" {
			return fmt.Errorf("llm: provider %s needs LLM_API_KEY", cfg.Provider)
		}
		return nil
	}
	switch cfg.Provider {
	case "fake":
		return NewFake(), nil
	case "ollama":
		return &ollama{base: or(cfg.BaseURL, "http://127.0.0.1:11434"), model: or(cfg.Model, "llama3.2"), embedModel: or(cfg.EmbedModel, "nomic-embed-text")}, nil
	case "anthropic":
		if err := need(); err != nil {
			return nil, err
		}
		return &anthropic{key: cfg.APIKey, base: or(cfg.BaseURL, "https://api.anthropic.com"), model: or(cfg.Model, "claude-sonnet-5")}, nil
	case "openai":
		if err := need(); err != nil {
			return nil, err
		}
		return &openai{key: cfg.APIKey, base: or(cfg.BaseURL, "https://api.openai.com"), model: or(cfg.Model, "gpt-5-mini"), embedModel: or(cfg.EmbedModel, "text-embedding-3-small")}, nil
	case "compatible":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("llm: provider compatible needs LLM_BASE_URL, the server's address (http://127.0.0.1:8080 for llama-server)")
		}
		// The paths add /v1; an address written with it works too.
		base := strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1")
		return &openai{name: "compatible", key: cfg.APIKey, base: base, model: or(cfg.Model, "default"), embedModel: or(cfg.EmbedModel, or(cfg.Model, "default"))}, nil
	case "google":
		if err := need(); err != nil {
			return nil, err
		}
		return &google{key: cfg.APIKey, base: or(cfg.BaseURL, "https://generativelanguage.googleapis.com"), model: or(cfg.Model, "gemini-2.5-flash"), embedModel: or(cfg.EmbedModel, "text-embedding-004")}, nil
	}
	return nil, fmt.Errorf("llm: unknown provider %q (one of %s)", cfg.Provider, strings.Join(Providers, ", "))
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// stripFences removes a ```json fence a model may wrap a document in.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// lastUser returns the last user message's text.
func lastUser(req Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == User {
			return req.Messages[i].Content
		}
	}
	return ""
}
