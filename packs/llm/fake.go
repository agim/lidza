package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
)

// Fake is the provider for tests and a first run: it answers with what a
// test scripted (Reply, ReplyJSON, ReplyToolCall), else echoes the last
// user message, and it remembers every request (Calls). Embeddings are
// deterministic. Configure with LLM_PROVIDER=fake (.env.test) and get it
// from the pack with llm.From(ctx).Fake().
type Fake struct {
	mu      sync.Mutex
	replies []Response
	calls   []Request
}

// NewFake returns an unscripted Fake.
func NewFake() *Fake { return &Fake{} }

func (f *Fake) Name() string { return "fake" }

// Reply queues a text reply.
func (f *Fake) Reply(text string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, Response{Text: text, Stop: "end"})
	return f
}

// ReplyJSON queues v as the JSON document a Generate call decodes.
func (f *Fake) ReplyJSON(v any) *Fake {
	data, _ := json.Marshal(v)
	return f.Reply(string(data))
}

// ReplyToolCall queues a reply that calls the tool with input.
func (f *Fake) ReplyToolCall(name string, input any) *Fake {
	data, _ := json.Marshal(input)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, Response{Stop: "tool", ToolCalls: []ToolCall{{ID: fmt.Sprintf("call_%d", len(f.calls)+len(f.replies)+1), Name: name, Input: data}}})
	return f
}

// Calls returns every request received, oldest first.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.calls...)
}

// Reset forgets scripted replies and recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies, f.calls = nil, nil
}

func (f *Fake) Chat(ctx context.Context, req Request, stream func(string) error) (Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	var res Response
	if len(f.replies) > 0 {
		res, f.replies = f.replies[0], f.replies[1:]
	} else {
		text := lastUser(req)
		if req.Schema != nil {
			text = "{}"
		} else {
			text = "fake: " + text
		}
		res = Response{Text: text, Stop: "end"}
	}
	f.mu.Unlock()
	res.Model = or(req.Model, "fake")
	res.Usage = Usage{Input: len(strings.Fields(req.System + " " + lastUser(req))), Output: len(strings.Fields(res.Text))}
	if stream != nil {
		for _, word := range strings.SplitAfter(res.Text, " ") {
			if err := stream(word); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// Embed returns an 8-dimensional vector per text, the same for the same
// text.
func (f *Fake) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		h := fnv.New64a()
		h.Write([]byte(t))
		sum := h.Sum64()
		v := make([]float32, 8)
		for j := range v {
			v[j] = float32((sum>>(uint(j)*8))&0xff)/255 - 0.5
		}
		out[i] = v
	}
	return out, nil
}
