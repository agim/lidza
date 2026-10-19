package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agim/lidza"
)

// stub serves one provider's API: it records the last request body and
// replies with what the test set. reply may be a function of the request.
type stub struct {
	srv   *httptest.Server
	last  map[string]any
	path  string
	head  http.Header
	reply func(req map[string]any) (int, string, string) // status, content type, body
}

func newStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.last = map[string]any{}
		json.Unmarshal(body, &s.last)
		s.path, s.head = r.URL.RequestURI(), r.Header.Clone()
		status, ct, reply := s.reply(s.last)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

var conversation = Request{System: "sys", Messages: []Message{
	{Role: User, Content: "what is the weather?"},
	{Role: Assistant, ToolCalls: []ToolCall{{ID: "c1", Name: "weather", Input: json.RawMessage(`{"city":"Tirana"}`)}}},
	{Role: ToolResult, ToolCallID: "c1", Name: "weather", Content: `{"temp":21}`},
}, Tools: []Tool{{Name: "weather", Description: "Weather.", Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}}}

func TestAnthropicWire(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "anthropic", APIKey: "k", BaseURL: s.srv.URL, Model: "claude-x"})
	ctx := context.Background()
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"21 degrees"}],"usage":{"input_tokens":10,"output_tokens":2}}`
	}
	res, err := l.Chat(ctx, conversation)
	if err != nil || res.Text != "21 degrees" || res.Usage.Input != 10 || res.Stop != "end" {
		t.Fatalf("%+v %v", res, err)
	}
	if s.head.Get("x-api-key") != "k" || s.head.Get("anthropic-version") == "" || s.path != "/v1/messages" {
		t.Fatalf("headers/path: %v %s", s.head, s.path)
	}
	msgs := s.last["messages"].([]any)
	if s.last["system"] != "sys" || len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" || msgs[2].(map[string]any)["role"] != "user" {
		t.Fatalf("messages: %s", js(s.last))
	}
	if blocks := msgs[2].(map[string]any)["content"].([]any); blocks[0].(map[string]any)["type"] != "tool_result" || blocks[0].(map[string]any)["tool_use_id"] != "c1" {
		t.Fatalf("tool result: %s", js(msgs[2]))
	}
	if tools := s.last["tools"].([]any); len(tools) != 1 || tools[0].(map[string]any)["input_schema"] == nil {
		t.Fatalf("tools: %s", js(s.last["tools"]))
	}

	// The model calls a tool.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"claude-x","stop_reason":"tool_use","content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Tirana"}}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	res, _ = l.Chat(ctx, conversation)
	if res.Stop != "tool" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "toolu_1" || string(res.ToolCalls[0].Input) != `{"city":"Tirana"}` || res.Text != "Let me check." {
		t.Fatalf("tool call: %+v", res)
	}

	// Structured output is output_config.format with a closed schema;
	// forced tool use is refused by the current models.
	s.reply = func(req map[string]any) (int, string, string) {
		if req["tool_choice"] != nil {
			return 400, "application/json", `{"error":"tool_choice type tool is not supported for this model"}`
		}
		format, _ := req["output_config"].(map[string]any)["format"].(map[string]any)
		if format["type"] != "json_schema" || format["schema"].(map[string]any)["additionalProperties"] != false {
			return 400, "application/json", `{"error":"bad format"}`
		}
		return 200, "application/json", `{"model":"claude-x","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":"{\"tags\":[\"a\"]}"}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	got, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}})
	if err != nil || len(got.Tags) != 1 || got.Tags[0] != "a" {
		t.Fatalf("generate: %+v %v", got, err)
	}

	// A refusal is Stop "refusal"; Generate says so.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"claude-x","stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber"},"content":[],"usage":{"input_tokens":1,"output_tokens":0}}`
	}
	if res, err := l.Chat(ctx, conversation); err != nil || res.Stop != "refusal" {
		t.Fatalf("refusal: %+v %v", res, err)
	}
	if _, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}}); !errors.Is(err, ErrRefused) {
		t.Fatalf("generate refusal: %v", err)
	}

	// Streaming.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "text/event-stream", strings.Join([]string{
			`event: message_start`, `data: {"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":5}}}`, ``,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, ``,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`, ``,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`, ``,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu","name":"weather","input":{}}}`, ``,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`, ``,
			`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Tirana\"}"}}`, ``,
			`data: {"type":"content_block_stop","index":1}`, ``,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`, ``,
			`data: {"type":"message_stop"}`, ``,
		}, "\n")
	}
	var streamed string
	res, err = l.Stream(ctx, conversation, func(s string) error { streamed += s; return nil })
	if err != nil || streamed != "Hello" || res.Text != "Hello" || res.Usage != (Usage{Input: 5, Output: 7}) || len(res.ToolCalls) != 1 || string(res.ToolCalls[0].Input) != `{"city":"Tirana"}` || res.Stop != "tool" {
		t.Fatalf("stream: %q %+v %v", streamed, res, err)
	}
	if s.last["stream"] != true {
		t.Fatal("stream flag not sent")
	}
	if _, err := l.Embed(ctx, []string{"x"}); err == nil {
		t.Fatal("anthropic embed should refuse")
	}
}

func TestOpenAIWire(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "openai", APIKey: "k", BaseURL: s.srv.URL, Model: "gpt-x"})
	ctx := context.Background()
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"gpt-x","choices":[{"finish_reason":"stop","message":{"content":"21 degrees"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`
	}
	res, err := l.Chat(ctx, conversation)
	if err != nil || res.Text != "21 degrees" || res.Usage.Input != 10 || res.Stop != "end" {
		t.Fatalf("%+v %v", res, err)
	}
	if s.head.Get("Authorization") != "Bearer k" || s.path != "/v1/chat/completions" {
		t.Fatalf("headers/path: %v %s", s.head, s.path)
	}
	msgs := s.last["messages"].([]any)
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" || msgs[3].(map[string]any)["tool_call_id"] != "c1" || s.last["max_completion_tokens"] != float64(4096) {
		t.Fatalf("messages: %s", js(s.last))
	}
	if calls := msgs[2].(map[string]any)["tool_calls"].([]any); calls[0].(map[string]any)["function"].(map[string]any)["arguments"] != `{"city":"Tirana"}` {
		t.Fatalf("assistant tool call: %s", js(msgs[2]))
	}
	if tools := s.last["tools"].([]any); tools[0].(map[string]any)["function"].(map[string]any)["parameters"] == nil {
		t.Fatalf("tools: %s", js(s.last["tools"]))
	}

	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"gpt-x","choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Tirana\"}"}}]}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
	}
	res, _ = l.Chat(ctx, conversation)
	if res.Stop != "tool" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_a" || string(res.ToolCalls[0].Input) != `{"city":"Tirana"}` {
		t.Fatalf("tool call: %+v", res)
	}

	s.reply = func(req map[string]any) (int, string, string) {
		rf := req["response_format"].(map[string]any)
		if rf["type"] != "json_schema" || rf["json_schema"].(map[string]any)["schema"] == nil {
			return 400, "application/json", `{"error":"no schema"}`
		}
		return 200, "application/json", `{"model":"gpt-x","choices":[{"finish_reason":"stop","message":{"content":"{\"tags\":[\"a\"]}"}}],"usage":{}}`
	}
	got, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}})
	if err != nil || len(got.Tags) != 1 {
		t.Fatalf("generate: %+v %v", got, err)
	}

	s.reply = func(map[string]any) (int, string, string) {
		return 200, "text/event-stream", strings.Join([]string{
			`data: {"model":"gpt-x","choices":[{"delta":{"content":"Hel"}}]}`, ``,
			`data: {"choices":[{"delta":{"content":"lo"}}]}`, ``,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_s","type":"function","function":{"name":"weather","arguments":"{\"ci"}}]}}]}`, ``,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"Tirana\"}"}}]}}]}`, ``,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, ``,
			`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":7}}`, ``,
			`data: [DONE]`, ``,
		}, "\n")
	}
	var streamed string
	res, err = l.Stream(ctx, conversation, func(s string) error { streamed += s; return nil })
	if err != nil || streamed != "Hello" || res.Usage != (Usage{Input: 5, Output: 7}) || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_s" || string(res.ToolCalls[0].Input) != `{"city":"Tirana"}` {
		t.Fatalf("stream: %q %+v %v", streamed, res, err)
	}

	s.reply = func(req map[string]any) (int, string, string) {
		if req["model"] != "text-embedding-3-small" || len(req["input"].([]any)) != 2 {
			return 400, "application/json", `{"error":"bad embed request"}`
		}
		return 200, "application/json", `{"data":[{"index":1,"embedding":[0.5]},{"index":0,"embedding":[0.1,0.2]}]}`
	}
	vectors, err := l.Embed(ctx, []string{"a", "b"})
	if err != nil || len(vectors) != 2 || len(vectors[0]) != 2 || vectors[1][0] != 0.5 || s.path != "/v1/embeddings" {
		t.Fatalf("embed: %v %v", vectors, err)
	}
}

func TestGoogleWire(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "google", APIKey: "k", BaseURL: s.srv.URL, Model: "gemini-x"})
	ctx := context.Background()
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"modelVersion":"gemini-x","candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"21 degrees"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}`
	}
	res, err := l.Chat(ctx, conversation)
	if err != nil || res.Text != "21 degrees" || res.Usage.Input != 10 || res.Stop != "end" {
		t.Fatalf("%+v %v", res, err)
	}
	if s.head.Get("x-goog-api-key") != "k" || s.path != "/v1beta/models/gemini-x:generateContent" {
		t.Fatalf("headers/path: %v %s", s.head, s.path)
	}
	contents := s.last["contents"].([]any)
	if len(contents) != 3 || contents[1].(map[string]any)["role"] != "model" || s.last["systemInstruction"] == nil {
		t.Fatalf("contents: %s", js(s.last))
	}
	if parts := contents[2].(map[string]any)["parts"].([]any); parts[0].(map[string]any)["functionResponse"].(map[string]any)["name"] != "weather" {
		t.Fatalf("function response: %s", js(contents[2]))
	}
	if decls := s.last["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any); decls[0].(map[string]any)["parameters"] == nil {
		t.Fatalf("tools: %s", js(s.last["tools"]))
	}

	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"Tirana"}}}]}}],"usageMetadata":{}}`
	}
	res, _ = l.Chat(ctx, conversation)
	if res.Stop != "tool" || len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "weather" || string(res.ToolCalls[0].Input) != `{"city":"Tirana"}` {
		t.Fatalf("tool call: %+v", res)
	}

	s.reply = func(req map[string]any) (int, string, string) {
		gen := req["generationConfig"].(map[string]any)
		if gen["responseMimeType"] != "application/json" || gen["responseSchema"] == nil {
			return 400, "application/json", `{"error":"no schema"}`
		}
		if js(gen["responseSchema"]) != `{"properties":{"tags":{"items":{"type":"string"},"type":"array"}},"required":["tags"],"type":"object"}` {
			return 400, "application/json", `{"error":"schema not cleaned: ` + js(gen["responseSchema"]) + `"}`
		}
		return 200, "application/json", `{"candidates":[{"content":{"parts":[{"text":"{\"tags\":[\"a\"]}"}]}}]}`
	}
	got, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}})
	if err != nil || len(got.Tags) != 1 {
		t.Fatalf("generate: %+v %v", got, err)
	}

	s.reply = func(map[string]any) (int, string, string) {
		return 200, "text/event-stream", strings.Join([]string{
			`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`, ``,
			`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7}}`, ``,
		}, "\n")
	}
	var streamed string
	res, err = l.Stream(ctx, conversation, func(s string) error { streamed += s; return nil })
	if err != nil || streamed != "Hello" || res.Text != "Hello" || res.Usage != (Usage{Input: 5, Output: 7}) || !strings.HasSuffix(s.path, ":streamGenerateContent?alt=sse") {
		t.Fatalf("stream: %q %+v %v %s", streamed, res, err, s.path)
	}

	// The default embedding model, at the 768 dimensions of the one it
	// replaced.
	s.reply = func(req map[string]any) (int, string, string) {
		reqs := req["requests"].([]any)
		if len(reqs) != 2 || reqs[0].(map[string]any)["outputDimensionality"] != float64(768) {
			return 400, "application/json", `{"error":"bad embed request"}`
		}
		return 200, "application/json", `{"embeddings":[{"values":[0.3,0.4]},{"values":[0.5]}]}`
	}
	vectors, err := l.Embed(ctx, []string{"a", "b"})
	if err != nil || len(vectors) != 2 || !strings.HasSuffix(s.path, "gemini-embedding-001:batchEmbedContents") {
		t.Fatalf("embed: %v %v %s", vectors, err, s.path)
	}
	// Shortened vectors come back at unit length.
	if v := vectors[0]; math.Abs(float64(v[0])-0.6) > 1e-6 || math.Abs(float64(v[1])-0.8) > 1e-6 || vectors[1][0] != 1 {
		t.Errorf("not normalised: %v", vectors)
	}

	// A safety stop is a refusal.
	for _, reply := range []string{
		`{"candidates":[{"finishReason":"SAFETY","content":{"parts":[]}}]}`,
		`{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`,
	} {
		s.reply = func(map[string]any) (int, string, string) { return 200, "application/json", reply }
		if res, err := l.Chat(ctx, conversation); err != nil || res.Stop != "refusal" {
			t.Errorf("%s: %+v %v", reply, res, err)
		}
	}
}

// Gemini 3 signs a function call; the signature goes back with it, and
// thought parts are not the answer.
func TestGoogleThoughtSignature(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "google", APIKey: "k", BaseURL: s.srv.URL})
	round := 0
	s.reply = func(req map[string]any) (int, string, string) {
		round++
		if round == 1 {
			return 200, "application/json", `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"Thinking about cities.","thought":true},{"functionCall":{"name":"weather","args":{"city":"Tirana"}},"thoughtSignature":"sig-1"}]}}]}`
		}
		call := req["contents"].([]any)[1].(map[string]any)["parts"].([]any)[0].(map[string]any)
		if call["thoughtSignature"] != "sig-1" {
			return 400, "application/json", `{"error":"Function call is missing a thought_signature"}`
		}
		return 200, "application/json", `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"21 degrees."}]}}]}`
	}
	res, err := l.Run(context.Background(), Request{Messages: []Message{{Role: User, Content: "weather?"}}}, []lidza.Tool{weatherTool()})
	if err != nil || res.Text != "21 degrees." {
		t.Fatalf("run: %+v %v", res, err)
	}
	if !strings.Contains(s.path, "/models/gemini-3.8-flash:") {
		t.Errorf("default model: %s", s.path)
	}
}

func TestOllamaWire(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "ollama", BaseURL: s.srv.URL, Model: "m", EmbedModel: "e"})
	ctx := context.Background()
	s.reply = func(req map[string]any) (int, string, string) {
		if req["stream"] != false || req["format"] == nil || req["options"].(map[string]any)["num_predict"] != float64(4096) {
			return 400, "application/json", `{"error":"bad request: ` + js(req) + `"}`
		}
		return 200, "application/json", `{"model":"m","message":{"role":"assistant","content":"{\"tags\":[\"a\"]}"},"done":true,"done_reason":"stop","prompt_eval_count":4,"eval_count":6}`
	}
	got, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}})
	if err != nil || len(got.Tags) != 1 {
		t.Fatalf("generate: %+v %v", got, err)
	}
	msgs := s.last["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages: %s", js(msgs))
	}

	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"m","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"weather","arguments":{"city":"Tirana"}}}]},"done":true,"done_reason":"stop"}`
	}
	res, _ := l.Chat(ctx, conversation)
	if res.Stop != "tool" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID == "" || string(res.ToolCalls[0].Input) != `{"city":"Tirana"}` {
		t.Fatalf("tool call: %+v", res)
	}
	if msgs := s.last["messages"].([]any); len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" || msgs[3].(map[string]any)["role"] != "tool" {
		t.Fatalf("messages: %s", js(msgs))
	}

	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/x-ndjson", "{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"Hel\"},\"done\":false}\n{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"lo\"},\"done\":false}\n{\"model\":\"m\",\"message\":{\"role\":\"assistant\",\"content\":\"\"},\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":5,\"eval_count\":7}\n"
	}
	var streamed string
	res, err = l.Stream(ctx, conversation, func(s string) error { streamed += s; return nil })
	if err != nil || streamed != "Hello" || res.Text != "Hello" || res.Usage != (Usage{Input: 5, Output: 7}) {
		t.Fatalf("stream: %q %+v %v", streamed, res, err)
	}

	s.reply = func(req map[string]any) (int, string, string) {
		if req["model"] != "e" {
			return 400, "application/json", `{"error":"wrong model"}`
		}
		return 200, "application/json", `{"embeddings":[[0.1],[0.2]]}`
	}
	vectors, err := l.Embed(ctx, []string{"a", "b"})
	if err != nil || len(vectors) != 2 || vectors[1][0] != 0.2 || s.path != "/api/embed" {
		t.Fatalf("embed: %v %v", vectors, err)
	}
}

// An OpenAI-compatible server (llama-server, vLLM, LM Studio): the
// address is required, the key optional and not sent when empty, an
// address written with /v1 works.
func TestCompatibleProvider(t *testing.T) {
	if _, err := New(Config{Provider: "compatible"}); err == nil || !strings.Contains(err.Error(), "LLM_BASE_URL") {
		t.Fatalf("no address: %v", err)
	}
	var auth, path, model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		model = body.Model
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer srv.Close()
	l, err := New(Config{Provider: "compatible", BaseURL: srv.URL + "/v1/", Model: "qwen2.5-7b"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.Chat(context.Background(), Request{Messages: []Message{{Role: User, Content: "hello"}}})
	if err != nil || res.Text != "hi there" {
		t.Fatalf("chat: %+v %v", res, err)
	}
	if auth != "" || path != "/v1/chat/completions" || model != "qwen2.5-7b" {
		t.Fatalf("request: auth %q path %q model %q", auth, path, model)
	}
	if l.Provider() != "compatible" {
		t.Fatalf("provider: %s", l.Provider())
	}
	l, _ = New(Config{Provider: "compatible", BaseURL: srv.URL, APIKey: "sk-local"})
	l.Chat(context.Background(), Request{Messages: []Message{{Role: User, Content: "hello"}}})
	if auth != "Bearer sk-local" || model != "default" {
		t.Fatalf("with a key: %q %q", auth, model)
	}
}

// A reply with thinking is sent back whole and in order on the next
// turn: the blocks are signed against what precedes them, and a tool
// loop that drops them is refused.
func TestAnthropicThinkingReplay(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "anthropic", APIKey: "k", BaseURL: s.srv.URL})
	ctx := context.Background()
	content := `[{"type":"thinking","thinking":"","signature":"s1"},{"type":"text","text":"Checking."},{"type":"tool_use","id":"t1","name":"weather","input":{"city":"Tirana"}},{"type":"thinking","thinking":"","signature":"s2"},{"type":"tool_use","id":"t2","name":"weather","input":{"city":"Durrës"}}]`
	round := 0
	s.reply = func(req map[string]any) (int, string, string) {
		round++
		if round == 1 {
			return 200, "application/json", `{"model":"claude-sonnet-5-5","stop_reason":"tool_use","content":` + content + `,"usage":{"input_tokens":1,"output_tokens":1}}`
		}
		msgs := req["messages"].([]any)
		var sent any
		json.Unmarshal([]byte(content), &sent)
		if got := js(msgs[1].(map[string]any)["content"]); got != js(sent) {
			return 400, "application/json", `{"error":"thinking blocks changed: ` + strings.ReplaceAll(got, `"`, `'`) + `"}`
		}
		if results := msgs[2].(map[string]any)["content"].([]any); len(results) != 2 {
			return 400, "application/json", `{"error":"want both results in one message"}`
		}
		return 200, "application/json", `{"model":"claude-sonnet-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"21 and 24."}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	res, err := l.Run(ctx, Request{Messages: []Message{{Role: User, Content: "weather?"}}}, []lidza.Tool{weatherTool()})
	if err != nil || res.Text != "21 and 24." || round != 2 {
		t.Fatalf("run: %+v %v (rounds %d)", res, err, round)
	}
	if s.last["model"] != "claude-sonnet-5-5" {
		t.Errorf("default model %v", s.last["model"])
	}

	// The same reply streamed is rebuilt block for block.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "text/event-stream", strings.Join([]string{
			`data: {"type":"message_start","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":5}}}`, ``,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`, ``,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Two "}}`, ``,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"cities."}}`, ``,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s1"}}`, ``,
			`data: {"type":"content_block_stop","index":0}`, ``,
			`data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque"}}`, ``,
			`data: {"type":"content_block_stop","index":1}`, ``,
			`data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`, ``,
			`data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Checking."}}`, ``,
			`data: {"type":"content_block_stop","index":2}`, ``,
			`data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"t1","name":"weather","input":{}}}`, ``,
			`data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Tirana\"}"}}`, ``,
			`data: {"type":"content_block_stop","index":3}`, ``,
			`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`, ``,
		}, "\n")
	}
	res, err = l.Stream(ctx, conversation, func(string) error { return nil })
	want := `[{"type":"thinking","thinking":"Two cities.","signature":"s1"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"Checking."},{"type":"tool_use","id":"t1","name":"weather","input":{"city":"Tirana"}}]`
	if err != nil || js(res.Raw) != want || res.Text != "Checking." || res.Stop != "tool" {
		t.Fatalf("stream raw:\n got %s\nwant %s\n%+v %v", js(res.Raw), want, res, err)
	}
	// A reply without thinking needs no Raw.
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"claude-haiku-4-5","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	if res, _ := l.Chat(ctx, conversation); res.Raw != nil {
		t.Errorf("raw without thinking: %s", js(res.Raw))
	}
}

func TestAnthropicTemperature(t *testing.T) {
	s := newStub(t)
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{}}`
	}
	ctx := context.Background()
	temp := 0.2
	for model, sent := range map[string]bool{"claude-sonnet-5-5": false, "claude-opus-5-5": false, "claude-opus-4-7": false, "claude-fable-5-1": false, "claude-haiku-4-5": true, "claude-sonnet-4-6": true, "claude-opus-4-6": true} {
		l, _ := New(Config{Provider: "anthropic", APIKey: "k", BaseURL: s.srv.URL, Model: model})
		if _, err := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "x"}}, Temperature: &temp}); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.last["temperature"]; ok != sent {
			t.Errorf("%s: temperature sent %v, want %v", model, ok, sent)
		}
	}
}

func TestAnthropicSchema(t *testing.T) {
	in := map[string]any{"type": "object", "properties": map[string]any{
		"name":  map[string]any{"type": "string", "maxLength": 80, "format": "email"},
		"price": map[string]any{"type": "string", "format": "decimal"},
		"blob":  map[string]any{"type": "string", "contentEncoding": "base64"},
		"tags":  map[string]any{"type": "array", "maxItems": 5, "items": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer", "minimum": 0}}}},
	}, "required": []any{"name"}}
	fitted, ok := anthropicSchema(in)
	got := js(fitted)
	if !ok {
		t.Error("a closed schema reported as not fitting")
	}
	want := `{"additionalProperties":false,"properties":{"blob":{"type":"string"},"name":{"format":"email","type":"string"},"price":{"type":"string"},"tags":{"items":{"additionalProperties":false,"properties":{"n":{"type":"integer"}},"type":"object"},"type":"array"}},"required":["name"],"type":"object"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func weatherTool() lidza.Tool {
	type in struct {
		City string `json:"city"`
	}
	return lidza.ToolFunc("weather", "Weather in a city.", func(ctx context.Context, i in) (map[string]any, error) {
		return map[string]any{"city": i.City, "temp": 21}, nil
	})
}

func TestOpenAIRefusalAndTemperature(t *testing.T) {
	s := newStub(t)
	ctx := context.Background()
	s.reply = func(map[string]any) (int, string, string) {
		return 200, "application/json", `{"model":"gpt-6-luna","choices":[{"finish_reason":"stop","message":{"content":null,"refusal":"I can't help with that."}}],"usage":{}}`
	}
	l, _ := New(Config{Provider: "openai", APIKey: "k", BaseURL: s.srv.URL})
	temp := 0.2
	res, err := l.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "x"}}, Temperature: &temp})
	if err != nil || res.Stop != "refusal" {
		t.Fatalf("refusal: %+v %v", res, err)
	}
	if s.last["model"] != "gpt-6-luna" {
		t.Errorf("default model %v", s.last["model"])
	}
	if _, ok := s.last["temperature"]; ok {
		t.Error("temperature sent to a reasoning model")
	}
	// A compatible server takes whatever model name it serves.
	c, _ := New(Config{Provider: "compatible", BaseURL: s.srv.URL, Model: "gpt-5-local"})
	c.Chat(ctx, Request{Messages: []Message{{Role: User, Content: "x"}}, Temperature: &temp})
	if s.last["temperature"] != 0.2 {
		t.Errorf("compatible: temperature %v", s.last["temperature"])
	}
}

// A type with a map has no closed schema: Anthropic gets the schema in
// the system prompt instead of output_config, and Generate checks the
// reply.
func TestAnthropicMapSchema(t *testing.T) {
	type counts struct {
		ByTag map[string]int `json:"byTag"`
	}
	s := newStub(t)
	s.reply = func(req map[string]any) (int, string, string) {
		if req["output_config"] != nil {
			return 400, "application/json", `{"error":"additionalProperties must be false"}`
		}
		if sys, _ := req["system"].(string); !strings.HasPrefix(sys, "Count tags.") || !strings.Contains(sys, `"byTag"`) {
			return 400, "application/json", `{"error":"no schema in the prompt"}`
		}
		return 200, "application/json", `{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"{\"byTag\":{\"go\":2}}"}],"usage":{}}`
	}
	l, _ := New(Config{Provider: "anthropic", APIKey: "k", BaseURL: s.srv.URL})
	got, err := Generate[counts](context.Background(), l, Request{System: "Count tags.", Messages: []Message{{Role: User, Content: "go go"}}})
	if err != nil || got.ByTag["go"] != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}
