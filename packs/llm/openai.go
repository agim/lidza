package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// openai speaks the Chat Completions API: POST /v1/chat/completions
// (SSE when streaming) and POST /v1/embeddings. A Schema uses
// response_format json_schema.
type openai struct{ key, base, model, embedModel string }

func (p *openai) Name() string { return "openai" }

func (p *openai) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + p.key}
}

type openaiToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (p *openai) Chat(ctx context.Context, req Request, stream func(string) error) (Response, error) {
	var msgs []map[string]any
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case ToolResult:
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content})
		case Assistant:
			om := map[string]any{"role": "assistant", "content": m.Content}
			if len(m.ToolCalls) > 0 {
				var calls []openaiToolCall
				for _, c := range m.ToolCalls {
					var tc openaiToolCall
					tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = c.ID, "function", c.Name, string(rawObject(c.Input))
					calls = append(calls, tc)
				}
				om["tool_calls"] = calls
				if m.Content == "" {
					om["content"] = nil
				}
			}
			msgs = append(msgs, om)
		default:
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
		}
	}
	body := map[string]any{"model": or(req.Model, p.model), "messages": msgs, "max_completion_tokens": req.MaxTokens}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		body["tools"] = tools
	}
	if req.Schema != nil {
		body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "output", "schema": req.Schema}}
	}
	if stream != nil {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	res, err := send(ctx, "openai", p.base+"/v1/chat/completions", p.headers(), body)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()

	type usage struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
	}
	finish := func(model, reason, text string, calls []openaiToolCall, u usage) Response {
		r := Response{Text: text, Model: model, Usage: Usage{Input: u.Prompt, Output: u.Completion}, Stop: "end"}
		for i, c := range calls {
			r.ToolCalls = append(r.ToolCalls, ToolCall{ID: or(c.ID, callID(i+1)), Name: c.Function.Name, Input: rawObject(json.RawMessage(c.Function.Arguments))})
		}
		switch {
		case len(r.ToolCalls) > 0:
			r.Stop = "tool"
		case reason == "length":
			r.Stop = "length"
		}
		return r
	}
	if stream == nil {
		var r struct {
			Model   string `json:"model"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Content   string           `json:"content"`
					ToolCalls []openaiToolCall `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
			Usage usage `json:"usage"`
		}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return Response{}, fmt.Errorf("llm: openai: decode reply: %w", err)
		}
		if len(r.Choices) == 0 {
			return Response{}, &Error{Provider: "openai", Status: 500, Body: "no choices in the reply"}
		}
		c := r.Choices[0]
		return finish(r.Model, c.FinishReason, c.Message.Content, c.Message.ToolCalls, r.Usage), nil
	}
	var text strings.Builder
	calls := map[int]*openaiToolCall{}
	var model, reason string
	var u usage
	err = readSSE(res.Body, func(data string) error {
		var ev struct {
			Model   string `json:"model"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content   string           `json:"content"`
					ToolCalls []openaiToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("llm: openai: decode stream: %w", err)
		}
		if ev.Model != "" {
			model = ev.Model
		}
		if ev.Usage != nil {
			u = *ev.Usage
		}
		for _, c := range ev.Choices {
			if c.FinishReason != "" {
				reason = c.FinishReason
			}
			if c.Delta.Content != "" {
				text.WriteString(c.Delta.Content)
				if err := stream(c.Delta.Content); err != nil {
					return err
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				cur, ok := calls[tc.Index]
				if !ok {
					cp := tc
					calls[tc.Index] = &cp
					continue
				}
				cur.Function.Arguments += tc.Function.Arguments
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				if tc.Function.Name != "" {
					cur.Function.Name = tc.Function.Name
				}
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	var ordered []openaiToolCall
	var idx []int
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		ordered = append(ordered, *calls[i])
	}
	return finish(model, reason, text.String(), ordered, u), nil
}

func (p *openai) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := postJSON(ctx, "openai", p.base+"/v1/embeddings", p.headers(), map[string]any{"model": or(model, p.embedModel), "input": texts}, &out); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vectors) {
			vectors[d.Index] = d.Embedding
		}
	}
	return vectors, nil
}
