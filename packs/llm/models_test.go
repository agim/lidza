package llm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestListModels(t *testing.T) {
	s := newStub(t)
	ctx := context.Background()
	ids := func(ms []Model) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return out
	}

	// Anthropic, a page at a time.
	s.reply = func(map[string]any) (int, string, string) {
		if strings.Contains(s.path, "after_id=claude-b") {
			return 200, "application/json", `{"data":[{"id":"claude-a","display_name":"A"}],"has_more":false,"last_id":"claude-a"}`
		}
		return 200, "application/json", `{"data":[{"id":"claude-c"},{"id":"claude-b"}],"has_more":true,"last_id":"claude-b"}`
	}
	ms, err := ListModels(ctx, Config{Provider: "anthropic", APIKey: "k", BaseURL: s.srv.URL})
	if err != nil || !reflect.DeepEqual(ids(ms), []string{"claude-a", "claude-b", "claude-c"}) || ms[0].Name != "A" || s.head.Get("x-api-key") != "k" {
		t.Fatalf("anthropic: %v %v", ms, err)
	}
	if !strings.HasPrefix(s.path, "/v1/models?") {
		t.Errorf("anthropic path %s", s.path)
	}

	// OpenAI: speech and image models are neither chat nor embedding.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"data":[{"id":"gpt-6-luna"},{"id":"text-embedding-3-small"},{"id":"tts-1"}]}`
	}
	ms, _ = ListModels(ctx, Config{Provider: "openai", APIKey: "k", BaseURL: s.srv.URL})
	want := map[string][2]bool{"gpt-6-luna": {true, false}, "text-embedding-3-small": {false, true}, "tts-1": {false, false}}
	for _, m := range ms {
		if w := want[m.ID]; w != [2]bool{m.Chat, m.Embed} {
			t.Errorf("openai %s: chat %v embed %v", m.ID, m.Chat, m.Embed)
		}
	}

	// Google: what each model supports.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"models":[{"name":"models/gemini-3.8-flash","displayName":"Gemini 3.8 Flash","supportedGenerationMethods":["generateContent","countTokens"]},{"name":"models/gemini-embedding-001","supportedGenerationMethods":["embedContent"]}]}`
	}
	ms, _ = ListModels(ctx, Config{Provider: "google", APIKey: "k", BaseURL: s.srv.URL})
	if len(ms) != 2 || ms[0].ID != "gemini-3.8-flash" || !ms[0].Chat || ms[0].Embed || !ms[1].Embed || ms[1].Chat || s.head.Get("x-goog-api-key") != "k" {
		t.Fatalf("google: %+v", ms)
	}

	// Ollama: the pulled tags; a name without a version is its :latest.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"models":[{"name":"llama3.2:latest"},{"name":"qwen2.5:0.5b"}]}`
	}
	ms, _ = ListModels(ctx, Config{Provider: "ollama", BaseURL: s.srv.URL})
	if !HasModel(ms, "llama3.2") || !HasModel(ms, "qwen2.5:0.5b") || HasModel(ms, "qwen2.5") || s.path != "/api/tags" {
		t.Fatalf("ollama: %+v %s", ms, s.path)
	}

	// A refused key is the provider's 401.
	s.reply = func(map[string]any) (int, string, string) {
		return 401, "application/json", `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	}
	_, err = ListModels(ctx, Config{Provider: "anthropic", APIKey: "bad", BaseURL: s.srv.URL})
	var e *Error
	if !errors.As(err, &e) || e.Status != 401 {
		t.Fatalf("refused key: %v", err)
	}
	if _, err := ListModels(ctx, Config{Provider: "fake"}); !errors.Is(err, ErrNoModelList) {
		t.Fatalf("fake: %v", err)
	}
	if _, err := ListEmbedModels(ctx, Config{Provider: "openai"}); err == nil || !strings.Contains(err.Error(), "EMBED_API_KEY") {
		t.Fatalf("embed key named: %v", err)
	}
}

func TestClosest(t *testing.T) {
	models := []Model{{ID: "claude-haiku-4-5"}, {ID: "claude-sonnet-5-5"}, {ID: "claude-opus-5-5"}, {ID: "claude-sonnet-5"}}
	if got := Closest(models, "claude-haiku-5", 2); got[0] != "claude-haiku-4-5" {
		t.Errorf("haiku: %v", got)
	}
	if got := Closest(models, "claude-sonet-5-5", 1); got[0] != "claude-sonnet-5-5" {
		t.Errorf("typo: %v", got)
	}
}
