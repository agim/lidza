package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/env"
)

// Chat on one provider, embeddings on another: each call goes to its own
// server with its own key, and the embedding reports its tokens.
func TestEmbedProvider(t *testing.T) {
	chat, embed := newStub(t), newStub(t)
	chat.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":3,"output_tokens":1}}`
	}
	embed.reply = func(req map[string]any) (int, string, string) {
		if req["model"] != "text-embedding-3-large" {
			return 400, "application/json", `{"error":"wrong model"}`
		}
		return 200, "application/json", `{"model":"text-embedding-3-large","data":[{"index":0,"embedding":[0.1]},{"index":1,"embedding":[0.2]}],"usage":{"prompt_tokens":7,"total_tokens":7}}`
	}
	l, err := New(Config{Provider: "anthropic", APIKey: "chat-key", BaseURL: chat.srv.URL,
		EmbedProvider: "openai", EmbedAPIKey: "embed-key", EmbedBaseURL: embed.srv.URL, EmbedModel: "text-embedding-3-large"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if chat.head.Get("x-api-key") != "chat-key" || chat.path != "/v1/messages" {
		t.Fatalf("chat went to %s with %v", chat.path, chat.head)
	}
	res, err := l.Embeddings(ctx, EmbedRequest{Texts: []string{"a", "b"}, Label: "post.index"})
	if err != nil || len(res.Vectors) != 2 || res.Vectors[1][0] != 0.2 || res.Usage.Input != 7 || res.Model != "text-embedding-3-large" {
		t.Fatalf("embeddings: %+v %v", res, err)
	}
	if embed.head.Get("Authorization") != "Bearer embed-key" || embed.path != "/v1/embeddings" {
		t.Fatalf("embed went to %s with %v", embed.path, embed.head)
	}
	if l.Provider() != "anthropic" || l.EmbedProvider() != "openai" {
		t.Fatalf("providers: %s %s", l.Provider(), l.EmbedProvider())
	}
	if vectors, err := l.Embed(ctx, []string{"a", "b"}); err != nil || len(vectors) != 2 {
		t.Fatalf("embed: %v %v", vectors, err)
	}
}

// Without EMBED_PROVIDER, Embed uses the chat provider, its key and its
// address; EMBED_MODEL wins over LLM_EMBED_MODEL, which still works.
func TestEmbedFallsBackToChat(t *testing.T) {
	s := newStub(t)
	var model string
	s.reply = func(req map[string]any) (int, string, string) {
		model, _ = req["model"].(string)
		return 200, "application/json", `{"data":[{"index":0,"embedding":[0.5]}],"usage":{"prompt_tokens":1}}`
	}
	ctx := context.Background()
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{}, "text-embedding-3-small"},
		{Config{LLMEmbedModel: "old-name"}, "old-name"},
		{Config{LLMEmbedModel: "old-name", EmbedModel: "new-name"}, "new-name"},
	} {
		c.cfg.Provider, c.cfg.APIKey, c.cfg.BaseURL = "openai", "k", s.srv.URL
		l, err := New(c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Embed(ctx, []string{"x"}); err != nil || model != c.want || s.head.Get("Authorization") != "Bearer k" {
			t.Fatalf("%+v: model %q, %v", c.cfg, model, err)
		}
		if l.EmbedProvider() != "openai" {
			t.Fatalf("embed provider: %s", l.EmbedProvider())
		}
	}
	// The same provider named twice shares the chat key and address.
	l, err := New(Config{Provider: "openai", APIKey: "k", BaseURL: s.srv.URL, EmbedProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Embed(ctx, []string{"x"}); err != nil || s.head.Get("Authorization") != "Bearer k" {
		t.Fatalf("same provider: %v %v", err, s.head)
	}
}

// Anthropic cannot embed: without EMBED_PROVIDER the pack still starts,
// and Embed names the setting that fixes it. none turns embeddings off.
func TestNoEmbeddings(t *testing.T) {
	ctx := context.Background()
	l, err := New(Config{Provider: "anthropic", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Embed(ctx, []string{"x"})
	if !errors.Is(err, ErrNoEmbeddings) || !strings.Contains(err.Error(), "EMBED_PROVIDER") {
		t.Fatalf("anthropic: %v", err)
	}
	if l.EmbedProvider() != "none" {
		t.Fatalf("embed provider: %s", l.EmbedProvider())
	}
	l, err = New(Config{Provider: "fake", EmbedProvider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Embeddings(ctx, EmbedRequest{Texts: []string{"x"}, Label: "post.index"}); !errors.Is(err, ErrNoEmbeddings) || !strings.Contains(err.Error(), "EMBED_PROVIDER=none") {
		t.Fatalf("none: %v", err)
	}
	// Chat is unaffected.
	if _, err := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	// Nothing to embed is not an error, whatever the provider.
	if v, err := l.Embed(ctx, nil); err != nil || v != nil {
		t.Fatalf("empty: %v %v", v, err)
	}
}

// The EMBED_ settings are checked at start as the LLM_ ones are, and the
// errors name them.
func TestEmbedConfig(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{EmbedProvider: "openai"}, "EMBED_API_KEY"},
		{Config{EmbedProvider: "google"}, "EMBED_API_KEY"},
		{Config{EmbedProvider: "compatible"}, "EMBED_BASE_URL"},
		{Config{EmbedProvider: "anthropic"}, "EMBED_PROVIDER"},
		{Config{EmbedProvider: "bard"}, "EMBED_PROVIDER"},
		// Another provider never borrows the chat provider's key.
		{Config{Provider: "google", APIKey: "k", EmbedProvider: "openai"}, "EMBED_API_KEY"},
	} {
		if _, err := New(c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.cfg, err)
		}
	}
	for _, p := range EmbedProviders {
		l, err := New(Config{Provider: "anthropic", APIKey: "k", EmbedProvider: p, EmbedAPIKey: "e", EmbedBaseURL: "http://127.0.0.1:1"})
		if err != nil || l.EmbedProvider() != p {
			t.Errorf("%s: %v %s", p, err, l.EmbedProvider())
		}
	}

	// The names the pack reads from .env and the credentials.
	var cfg Config
	if err := env.Fill(&cfg, map[string]string{"EMBED_PROVIDER": "ollama", "EMBED_MODEL": "m", "LLM_EMBED_MODEL": "old", "EMBED_API_KEY": "k", "EMBED_BASE_URL": "http://h"}); err != nil {
		t.Fatal(err)
	}
	if cfg.EmbedProvider != "ollama" || cfg.EmbedModel != "m" || cfg.LLMEmbedModel != "old" || cfg.EmbedAPIKey != "k" || cfg.EmbedBaseURL != "http://h" {
		t.Fatalf("env: %+v", cfg)
	}
}

// Reconfigure switches the embeddings provider as it does the chat one:
// the admin pages save EMBED_PROVIDER and apply it without a restart.
func TestReconfigureEmbed(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LLM_PROVIDER", "fake")
	l, err := New(Config{Provider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if l.EmbedProvider() != "fake" {
		t.Fatalf("before: %s", l.EmbedProvider())
	}
	t.Setenv("EMBED_PROVIDER", "none")
	if err := l.Reconfigure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Embed(context.Background(), []string{"x"}); !errors.Is(err, ErrNoEmbeddings) || l.EmbedProvider() != "none" {
		t.Fatalf("after: %s %v", l.EmbedProvider(), err)
	}
	t.Setenv("EMBED_PROVIDER", "openai")
	if err := l.Reconfigure(context.Background()); err == nil || !strings.Contains(err.Error(), "EMBED_API_KEY") {
		t.Fatalf("a refused setting: %v", err)
	}
	if l.EmbedProvider() != "none" {
		t.Fatalf("a refused setting changed the provider: %s", l.EmbedProvider())
	}
}
