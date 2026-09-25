package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	// Structured output rides a forced tool.
	s.reply = func(req map[string]any) (int, string, string) {
		if req["tool_choice"].(map[string]any)["name"] != structuredTool {
			return 400, "application/json", `{"error":"no forced tool"}`
		}
		return 200, "application/json", `{"model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t","name":"structured_output","input":{"tags":["a"]}}],"usage":{"input_tokens":1,"output_tokens":1}}`
	}
	got, err := Generate[tags](ctx, l, Request{Messages: []Message{{Role: User, Content: "x"}}})
	if err != nil || len(got.Tags) != 1 || got.Tags[0] != "a" {
		t.Fatalf("generate: %+v %v", got, err)
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
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" || msgs[3].(map[string]any)["tool_call_id"] != "c1" || s.last["max_completion_tokens"] != float64(1024) {
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

	s.reply = func(req map[string]any) (int, string, string) {
		if len(req["requests"].([]any)) != 2 {
			return 400, "application/json", `{"error":"bad embed request"}`
		}
		return 200, "application/json", `{"embeddings":[{"values":[0.1,0.2]},{"values":[0.5]}]}`
	}
	vectors, err := l.Embed(ctx, []string{"a", "b"})
	if err != nil || len(vectors) != 2 || vectors[1][0] != 0.5 || !strings.HasSuffix(s.path, "text-embedding-004:batchEmbedContents") {
		t.Fatalf("embed: %v %v %s", vectors, err, s.path)
	}
}

func TestOllamaWire(t *testing.T) {
	s := newStub(t)
	l, _ := New(Config{Provider: "ollama", BaseURL: s.srv.URL, Model: "m", EmbedModel: "e"})
	ctx := context.Background()
	s.reply = func(req map[string]any) (int, string, string) {
		if req["stream"] != false || req["format"] == nil || req["options"].(map[string]any)["num_predict"] != float64(1024) {
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
