package llm

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
	"github.com/agim/lidza/pkg/lidzatest"
)

// TestOllamaLive runs against a local Ollama when one answers on
// LLM_OLLAMA_URL (default http://127.0.0.1:11434) with the model in
// LLM_OLLAMA_MODEL (default qwen2.5:0.5b, 400 MB) and the embedding model
// in LLM_OLLAMA_EMBED_MODEL (default all-minilm, 45 MB); skipped otherwise.
func TestOllamaLive(t *testing.T) {
	base := or(os.Getenv("LLM_OLLAMA_URL"), "http://127.0.0.1:11434")
	model := or(os.Getenv("LLM_OLLAMA_MODEL"), "qwen2.5:0.5b")
	embedModel := or(os.Getenv("LLM_OLLAMA_EMBED_MODEL"), "all-minilm")
	res, err := (&http.Client{Timeout: 2 * time.Second}).Get(base + "/api/tags")
	if err != nil {
		t.Skipf("no Ollama at %s: %v", base, err)
	}
	res.Body.Close()
	l, err := New(Config{Provider: "ollama", BaseURL: base, Model: model, EmbedModel: embedModel, MaxTokens: 64, Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	zero := 0.0
	reply, err := l.Chat(ctx, Request{System: "Answer with one word.", Messages: []Message{{Role: User, Content: "What color is the sky on a clear day?"}}, Temperature: &zero})
	if err != nil || !strings.Contains(strings.ToLower(reply.Text), "blue") || reply.Usage.Output == 0 {
		t.Fatalf("chat: %+v %v", reply, err)
	}
	var streamed strings.Builder
	sres, err := l.Stream(ctx, Request{Messages: []Message{{Role: User, Content: "Count from 1 to 5, digits separated by spaces."}}, Temperature: &zero}, func(s string) error { streamed.WriteString(s); return nil })
	if err != nil || streamed.String() != sres.Text || !strings.Contains(sres.Text, "3") {
		t.Fatalf("stream: %q %+v %v", streamed.String(), sres, err)
	}
	type answer struct {
		Capital string `json:"capital"`
		Country string `json:"country"`
	}
	// A small model's knowledge is thin; the point is a reply shaped by
	// the schema, so the question is one it knows.
	got, err := Generate[answer](ctx, l, Request{Messages: []Message{{Role: User, Content: "The capital of France. Reply as JSON with the fields capital and country."}}, Temperature: &zero})
	if err != nil || !strings.Contains(strings.ToLower(got.Capital), "paris") || got.Country == "" {
		t.Fatalf("generate: %+v %v", got, err)
	}
	vectors, err := l.Embed(ctx, []string{"a cat", "a dog", "the stock market"})
	if err != nil || len(vectors) != 3 || len(vectors[0]) < 64 {
		t.Fatalf("embed: %d vectors %v", len(vectors), err)
	}
	if cos(vectors[0], vectors[1]) <= cos(vectors[0], vectors[2]) {
		t.Errorf("embeddings: cat should be closer to dog than to the stock market")
	}
	// Tools: a small model may or may not call one; the loop must hold.
	weather := lidza.ToolFunc("weather", "The current weather in a city.", func(ctx context.Context, in struct {
		City string `json:"city"`
	}) (map[string]any, error) {
		return map[string]any{"city": in.City, "celsius": 21, "sky": "clear"}, nil
	})
	rres, err := l.Run(ctx, Request{System: "Use the weather tool to answer, then answer in one sentence.", Messages: []Message{{Role: User, Content: "What is the weather in Tirana right now?"}}, Temperature: &zero}, []lidza.Tool{weather})
	if err != nil || rres.Text == "" {
		t.Fatalf("run: %+v %v", rres, err)
	}
	t.Logf("ollama %s: chat %q, run %q (tool rounds through %d tokens)", model, reply.Text, rres.Text, rres.Usage.Input)
}

// TestRecorded replays exchanges recorded against the hosted providers
// (testdata/http/llm-<provider>.json). Recording needs the key:
//
//	LIDZA_RECORD=1 LLM_API_KEY_ANTHROPIC=... go test ./packs/llm -run TestRecorded/anthropic
//
// Without a fixture the provider's case is skipped, so the wire format is
// proven only by the stubs in providers_test.go until someone records.
func TestRecorded(t *testing.T) {
	for _, provider := range []string{"anthropic", "openai", "google"} {
		t.Run(provider, func(t *testing.T) {
			fixture := filepath.Join(lidzatest.FixtureDir, "llm-"+provider+".json")
			key := os.Getenv("LLM_API_KEY_" + strings.ToUpper(provider))
			if _, err := os.Stat(fixture); err != nil && (os.Getenv(lidzatest.RecordEnv) != "1" || key == "") {
				t.Skipf("no fixture %s (record it with %s=1 and LLM_API_KEY_%s)", fixture, lidzatest.RecordEnv, strings.ToUpper(provider))
			}
			rec, err := lidzatest.NewRecorder("llm-" + provider)
			if err != nil {
				t.Fatal(err)
			}
			s := lidza.NewServices()
			lidza.Provide[http.RoundTripper](s, rec)
			ctx := lidza.WithServices(context.Background(), s)
			l, err := New(Config{Provider: provider, APIKey: or(key, "recorded"), MaxTokens: 64})
			if err != nil {
				t.Fatal(err)
			}
			zero := 0.0
			reply, err := l.Chat(ctx, Request{System: "Answer with one word.", Messages: []Message{{Role: User, Content: "What color is the sky on a clear day?"}}, Temperature: &zero})
			if err != nil || !strings.Contains(strings.ToLower(reply.Text), "blue") || reply.Usage.Output == 0 {
				t.Fatalf("chat: %+v %v", reply, err)
			}
			type answer struct {
				Capital string `json:"capital"`
			}
			got, err := Generate[answer](ctx, l, Request{Messages: []Message{{Role: User, Content: "The capital of Albania."}}, Temperature: &zero})
			if err != nil || !strings.Contains(strings.ToLower(got.Capital), "tirana") {
				t.Fatalf("generate: %+v %v", got, err)
			}
			weather := lidza.ToolFunc("weather", "The current weather in a city.", func(ctx context.Context, in struct {
				City string `json:"city"`
			}) (map[string]any, error) {
				return map[string]any{"celsius": 21, "sky": "clear"}, nil
			})
			rres, err := l.Run(ctx, Request{Messages: []Message{{Role: User, Content: "Use the weather tool: what is the weather in Tirana right now? One sentence."}}, Temperature: &zero}, []lidza.Tool{weather})
			if err != nil || !strings.Contains(rres.Text, "21") {
				t.Fatalf("run: %+v %v", rres, err)
			}
			var streamed strings.Builder
			if _, err := l.Stream(ctx, Request{Messages: []Message{{Role: User, Content: "Count from 1 to 5."}}, Temperature: &zero}, func(s string) error { streamed.WriteString(s); return nil }); err != nil || !strings.Contains(streamed.String(), "3") {
				t.Fatalf("stream: %q %v", streamed.String(), err)
			}
			if provider != "anthropic" {
				if vectors, err := l.Embed(ctx, []string{"a cat", "a dog"}); err != nil || len(vectors) != 2 || len(vectors[0]) < 64 {
					t.Fatalf("embed: %d %v", len(vectors), err)
				}
			}
		})
	}
}

func cos(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	return dot / (na * nb)
}
