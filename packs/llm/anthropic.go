package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// anthropic speaks the Messages API: POST /v1/messages, content blocks
// for text, tool_use and tool_result, SSE when streaming. A Schema goes
// in output_config.format. A reply with thinking blocks is kept whole in
// Response.Raw: the blocks are signed against what precedes them, so a
// tool loop sends the reply back unchanged. No embeddings.
type anthropic struct{ key, base, model string }

func (p *anthropic) Name() string { return "anthropic" }

func (p *anthropic) headers() map[string]string {
	return map[string]string{"x-api-key": p.key, "anthropic-version": "2023-06-01"}
}

func (p *anthropic) Chat(ctx context.Context, req Request, stream func(string) error) (Response, error) {
	var msgs []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case ToolResult:
			block := map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}
			// Consecutive results share one user message, as the API wants.
			if n := len(msgs); n > 0 && msgs[n-1]["role"] == "user" {
				if blocks, ok := msgs[n-1]["content"].([]map[string]any); ok && len(blocks) > 0 && blocks[0]["type"] == "tool_result" {
					msgs[n-1]["content"] = append(blocks, block)
					continue
				}
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": []map[string]any{block}})
		case Assistant:
			if len(m.Raw) > 0 {
				msgs = append(msgs, map[string]any{"role": "assistant", "content": m.Raw})
				continue
			}
			var blocks []map[string]any
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, c := range m.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": rawObject(c.Input)})
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})
		default:
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Content})
		}
	}
	body := map[string]any{"model": or(req.Model, p.model), "max_tokens": req.MaxTokens, "messages": msgs}
	if req.System != "" {
		body["system"] = req.System
	}
	if req.Temperature != nil && anthropicSamples(or(req.Model, p.model)) {
		body["temperature"] = *req.Temperature
	}
	var tools []map[string]any
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	if req.Schema != nil {
		body["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": anthropicSchema(req.Schema)}}
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if stream != nil {
		body["stream"] = true
	}
	res, err := send(ctx, "anthropic", p.base+"/v1/messages", p.headers(), body)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()

	type block struct {
		Type      string          `json:"type"`
		Text      string          `json:"text,omitempty"`
		ID        string          `json:"id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Input     json.RawMessage `json:"input,omitempty"`
		Thinking  *string         `json:"thinking,omitempty"`
		Signature *string         `json:"signature,omitempty"`
		Data      string          `json:"data,omitempty"`
		raw       json.RawMessage
	}
	finish := func(model, stopReason string, blocks []block, in, out int) Response {
		r := Response{Model: model, Usage: Usage{Input: in, Output: out}, Stop: "end"}
		var text strings.Builder
		thinking := false
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "tool_use":
				r.ToolCalls = append(r.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: rawObject(b.Input)})
			case "thinking", "redacted_thinking":
				thinking = true
			}
		}
		r.Text = text.String()
		if thinking {
			for _, b := range blocks {
				r.Raw = append(r.Raw, b.raw)
			}
		}
		switch {
		case stopReason == "refusal":
			r.Stop = "refusal"
		case len(r.ToolCalls) > 0:
			r.Stop = "tool"
		case stopReason == "max_tokens":
			r.Stop = "length"
		}
		return r
	}
	if stream == nil {
		var r struct {
			Model      string            `json:"model"`
			StopReason string            `json:"stop_reason"`
			Content    []json.RawMessage `json:"content"`
			Usage      struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return Response{}, fmt.Errorf("llm: anthropic: decode reply: %w", err)
		}
		blocks := make([]block, len(r.Content))
		for i, c := range r.Content {
			if err := json.Unmarshal(c, &blocks[i]); err != nil {
				return Response{}, fmt.Errorf("llm: anthropic: decode reply: %w", err)
			}
			blocks[i].raw = c
		}
		return finish(r.Model, r.StopReason, blocks, r.Usage.Input, r.Usage.Output), nil
	}
	var blocks []block
	var partial []strings.Builder
	var model, stopReason string
	var in, out int
	err = readSSE(res.Body, func(data string) error {
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					Input int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock block `json:"content_block"`
			Delta        struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				Output int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("llm: anthropic: decode stream: %w", err)
		}
		switch ev.Type {
		case "message_start":
			model, in = ev.Message.Model, ev.Message.Usage.Input
		case "content_block_start":
			for len(blocks) <= ev.Index {
				blocks = append(blocks, block{})
				partial = append(partial, strings.Builder{})
			}
			blocks[ev.Index] = ev.ContentBlock
		case "content_block_delta":
			if ev.Index >= len(blocks) {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				blocks[ev.Index].Text += ev.Delta.Text
				return stream(ev.Delta.Text)
			case "input_json_delta":
				partial[ev.Index].WriteString(ev.Delta.PartialJSON)
			case "thinking_delta":
				partial[ev.Index].WriteString(ev.Delta.Thinking)
			case "signature_delta":
				sig := ev.Delta.Signature
				blocks[ev.Index].Signature = &sig
			}
		case "content_block_stop":
			if ev.Index >= len(blocks) {
				return nil
			}
			b := &blocks[ev.Index]
			switch b.Type {
			case "tool_use":
				b.Input = rawObject(json.RawMessage(partial[ev.Index].String()))
			case "thinking":
				t := partial[ev.Index].String()
				b.Thinking = &t
				if b.Signature == nil {
					b.Signature = new(string)
				}
			}
			raw, err := json.Marshal(b)
			if err != nil {
				return fmt.Errorf("llm: anthropic: encode block: %w", err)
			}
			b.raw = raw
		case "message_delta":
			stopReason, out = ev.Delta.StopReason, ev.Usage.Output
		case "error":
			return &Error{Provider: "anthropic", Status: 500, Body: ev.Error.Message}
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	return finish(model, stopReason, blocks, in, out), nil
}

func (p *anthropic) Embed(context.Context, string, []string) ([][]float32, error) {
	return nil, errAnthropicEmbed
}

// anthropicSamples reports whether the model takes sampling settings:
// Claude Opus 4.7, Sonnet 5 and every model after them refuse a
// temperature (400), so only the older families get one.
func anthropicSamples(model string) bool {
	for _, prefix := range []string{"claude-3", "claude-haiku-4", "claude-sonnet-4", "claude-opus-4-0", "claude-opus-4-1", "claude-opus-4-2", "claude-opus-4-5", "claude-opus-4-6"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

// anthropicSchema fits a JSON Schema to structured outputs: every object
// closed (additionalProperties false, so a Go map comes back empty), no
// numeric, length or count limits and only the formats the API knows.
// Generate still checks the reply against the type's own rules.
func anthropicSchema(s map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range s {
		switch k {
		case "$schema", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties",
			"contentEncoding":
			continue
		case "format":
			if f, _ := v.(string); anthropicFormats[f] {
				out[k] = v
			}
		case "properties", "$defs", "definitions":
			if m, ok := v.(map[string]any); ok {
				sub := map[string]any{}
				for name, prop := range m {
					if pm, ok := prop.(map[string]any); ok {
						sub[name] = anthropicSchema(pm)
					} else {
						sub[name] = prop
					}
				}
				out[k] = sub
				continue
			}
			out[k] = v
		case "items", "additionalProperties":
			if m, ok := v.(map[string]any); ok {
				out[k] = anthropicSchema(m)
				continue
			}
			out[k] = v
		case "anyOf", "allOf", "oneOf":
			if list, ok := v.([]any); ok {
				subs := make([]any, len(list))
				for i, item := range list {
					if m, ok := item.(map[string]any); ok {
						subs[i] = anthropicSchema(m)
					} else {
						subs[i] = item
					}
				}
				out[k] = subs
				continue
			}
			out[k] = v
		default:
			out[k] = v
		}
	}
	if isObjectSchema(out) {
		out["additionalProperties"] = false
	}
	return out
}

// anthropicFormats are the string formats structured outputs accept;
// another (decimal) is dropped and left to Generate's check.
var anthropicFormats = map[string]bool{"date-time": true, "time": true, "date": true, "duration": true, "email": true, "hostname": true, "uri": true, "ipv4": true, "ipv6": true, "uuid": true}

func isObjectSchema(s map[string]any) bool {
	switch t := s["type"].(type) {
	case string:
		return t == "object"
	case []any:
		for _, x := range t {
			if x == "object" {
				return true
			}
		}
	}
	return false
}
