package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agim/lidza"
)

type tags struct {
	Tags []string `json:"tags"`
}

func (t tags) Validate() error {
	if len(t.Tags) == 0 {
		return errors.New("tags: at least one")
	}
	return nil
}

func fakeLLM(t *testing.T) (*LLM, *Fake) {
	t.Helper()
	l, err := New(Config{Provider: "fake", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	return l, l.Fake()
}

func TestFakeChatGenerateStream(t *testing.T) {
	l, f := fakeLLM(t)
	ctx := context.Background()
	res, err := l.Chat(ctx, Request{System: "Be brief.", Messages: []Message{{Role: User, Content: "hello"}}})
	if err != nil || res.Text != "fake: hello" || res.Model != "test-model" || res.Stop != "end" || res.Usage.Output == 0 {
		t.Fatalf("echo: %+v %v", res, err)
	}
	f.Reply("scripted")
	if res, _ := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "x"}}}); res.Text != "scripted" {
		t.Fatalf("scripted: %+v", res)
	}
	if calls := f.Calls(); len(calls) != 2 || calls[0].System != "Be brief." || calls[0].MaxTokens != 1024 {
		t.Fatalf("calls: %+v", calls)
	}

	f.ReplyJSON(tags{Tags: []string{"go", "rust"}})
	got, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "tag it"}}})
	if err != nil || strings.Join(got.Tags, ",") != "go,rust" {
		t.Fatalf("generate: %+v %v", got, err)
	}
	if last := f.Calls()[2]; last.Schema == nil || last.Schema["type"] != "object" {
		t.Fatalf("generate sent no schema: %+v", last)
	}
	f.Reply("```json\n{\"tags\": []}\n```")
	if _, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "tag it"}}}); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("invalid reply accepted: %v", err)
	}
	f.Reply("not json")
	if _, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}}); err == nil {
		t.Fatal("non-JSON reply accepted")
	}

	f.Reply("one two three")
	var parts []string
	res, err = l.Stream(ctx, Request{Messages: []Message{{Role: User, Content: "x"}}}, func(s string) error { parts = append(parts, s); return nil })
	if err != nil || strings.Join(parts, "") != "one two three" || res.Text != "one two three" {
		t.Fatalf("stream: %v %v %+v", parts, err, res)
	}

	vectors, err := l.Embed(ctx, []string{"a", "b", "a"})
	if err != nil || len(vectors) != 3 || len(vectors[0]) != 8 || vectors[0][0] != vectors[2][0] || vectors[0][1] == vectors[1][1] && vectors[0][0] == vectors[1][0] {
		t.Fatalf("embed: %v %v", vectors, err)
	}
	stats := l.TelemetryStats()
	if stats["calls"] != 7 || stats["failures"] != 0 || stats["output_tokens"] == 0 {
		t.Fatalf("stats: %v", stats)
	}
}

func TestRunTools(t *testing.T) {
	l, f := fakeLLM(t)
	type in struct {
		City string `json:"city"`
	}
	var called atomic.Int32
	weather := lidza.ToolFunc("weather", "Weather in a city.", func(ctx context.Context, i in) (map[string]any, error) {
		called.Add(1)
		return map[string]any{"city": i.City, "temp": 21}, nil
	})
	tools := ToolsOf(weather)
	if len(tools) != 1 || tools[0].Name != "weather" || tools[0].Schema["properties"].(map[string]any)["city"] == nil {
		t.Fatalf("tools: %+v", tools)
	}
	f.ReplyToolCall("weather", in{City: "Tirana"}).ReplyToolCall("nope", nil).Reply("21 degrees in Tirana")
	res, err := l.Run(context.Background(), Request{Messages: []Message{{Role: User, Content: "weather in Tirana?"}}}, []lidza.Tool{weather})
	if err != nil || res.Text != "21 degrees in Tirana" || called.Load() != 1 || res.Usage.Input == 0 {
		t.Fatalf("run: %+v %v called=%d", res, err, called.Load())
	}
	calls := f.Calls()
	if len(calls) != 3 || len(calls[0].Tools) != 1 {
		t.Fatalf("rounds: %d", len(calls))
	}
	// The second round carries the assistant's call and the tool's result;
	// the third the refusal of an unknown tool.
	msgs := calls[1].Messages
	if len(msgs) != 3 || msgs[1].Role != Assistant || len(msgs[1].ToolCalls) != 1 || msgs[2].Role != ToolResult || msgs[2].Name != "weather" || !strings.Contains(msgs[2].Content, `"temp":21`) {
		t.Fatalf("round 2 messages: %+v", msgs)
	}
	if last := calls[2].Messages[len(calls[2].Messages)-1]; !strings.Contains(last.Content, "no tool named nope") {
		t.Fatalf("unknown tool: %+v", last)
	}
	// A model that never answers hits the round limit.
	l.cfg.MaxToolRounds = 2
	f.Reset()
	f.ReplyToolCall("weather", in{}).ReplyToolCall("weather", in{}).ReplyToolCall("weather", in{})
	if _, err := l.Run(context.Background(), Request{Messages: []Message{{Role: User, Content: "x"}}}, []lidza.Tool{weather}); err == nil || !strings.Contains(err.Error(), "LLM_MAX_TOOL_ROUNDS") {
		t.Fatalf("round limit: %v", err)
	}
}

// TestRetry: 429 and 5xx are retried with the provider's Retry-After, a
// 400 is not, and a streamed call is not retried once text went out.
func TestRetry(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "7")
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case 2:
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			w.Write([]byte(`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":1}`))
		}
	}))
	defer srv.Close()
	l, err := New(Config{Provider: "ollama", BaseURL: srv.URL, Model: "m", MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	l.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	res, err := l.Chat(context.Background(), Request{Messages: []Message{{Role: User, Content: "x"}}})
	if err != nil || res.Text != "ok" || hits.Load() != 3 || len(waits) != 2 || waits[0] != 7*time.Second || waits[1] != 2*time.Second {
		t.Fatalf("retry: %+v %v hits=%d waits=%v", res, err, hits.Load(), waits)
	}
	if stats := l.TelemetryStats(); stats["calls"] != 1 || stats["input_tokens"] != 3 {
		t.Fatalf("stats: %v", stats)
	}

	hits.Store(0)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	}))
	defer bad.Close()
	l, _ = New(Config{Provider: "ollama", BaseURL: bad.URL, MaxAttempts: 3})
	l.sleep = func(context.Context, time.Duration) error { return nil }
	var e *Error
	if _, err := l.Chat(context.Background(), Request{Messages: []Message{{Role: User, Content: "x"}}}); !errors.As(err, &e) || e.Status != http.StatusBadRequest || e.Retryable() || hits.Load() != 1 {
		t.Fatalf("400: %v hits=%d", err, hits.Load())
	}

	hits.Store(0)
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"partial\"},\"done\":false}\n"))
		w.Write([]byte("not json\n"))
	}))
	defer cut.Close()
	l, _ = New(Config{Provider: "ollama", BaseURL: cut.URL, MaxAttempts: 3})
	l.sleep = func(context.Context, time.Duration) error { return nil }
	var got string
	if _, err := l.Stream(context.Background(), Request{Messages: []Message{{Role: User, Content: "x"}}}, func(s string) error { got += s; return nil }); err == nil || hits.Load() != 1 || got != "partial" {
		t.Fatalf("stream after output must not retry: %v hits=%d got=%q", err, hits.Load(), got)
	}
}

func TestProviderConfig(t *testing.T) {
	for _, p := range []string{"anthropic", "openai", "google"} {
		if _, err := New(Config{Provider: p}); err == nil || !strings.Contains(err.Error(), "LLM_API_KEY") {
			t.Errorf("%s without a key: %v", p, err)
		}
		if l, err := New(Config{Provider: p, APIKey: "k"}); err != nil || l.Provider() != p {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := New(Config{Provider: "bard"}); err == nil {
		t.Error("unknown provider accepted")
	}
	if l, _ := New(Config{}); l.Provider() != "fake" || l.Fake() == nil {
		t.Error("default provider is fake")
	}
}

func TestSSE(t *testing.T) {
	var got []string
	err := readSSE(strings.NewReader("event: a\ndata: one\n\ndata: two\ndata: lines\n\n: comment\n\ndata: [DONE]\n\ndata: after\n\n"), func(d string) error {
		got = append(got, d)
		return nil
	})
	if err != nil || strings.Join(got, "|") != "one|two\nlines" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestGoogleSchema(t *testing.T) {
	in := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"name":  map[string]any{"type": []any{"string", "null"}, "format": "x"},
		"items": map[string]any{"type": "array", "items": map[string]any{"type": "string", "contentEncoding": "base64"}},
	}, "required": []string{"name"}}
	out := googleSchema(in)
	data, _ := json.Marshal(out)
	s := string(data)
	for _, bad := range []string{"additionalProperties", "format", "contentEncoding", `"null"`} {
		if strings.Contains(s, bad) {
			t.Errorf("kept %s: %s", bad, s)
		}
	}
	if !strings.Contains(s, `"nullable":true`) || !strings.Contains(s, `"required":["name"]`) {
		t.Errorf("schema: %s", s)
	}
}
