package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// anthropic speaks the Messages API: POST /v1/messages, content blocks
// for text, tool_use and tool_result, SSE when streaming. A Schema is
// enforced through a forced tool whose input is the document. No
// embeddings.
type anthropic struct{ key, base, model string }

func (p *anthropic) Name() string { return "anthropic" }

const structuredTool = "structured_output"

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
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	var tools []map[string]any
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
	}
	if req.Schema != nil {
		tools = append(tools, map[string]any{"name": structuredTool, "description": "Return the answer as this document.", "input_schema": req.Schema})
		body["tool_choice"] = map[string]any{"type": "tool", "name": structuredTool}
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
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	finish := func(model, stopReason string, blocks []block, in, out int) Response {
		r := Response{Model: model, Usage: Usage{Input: in, Output: out}, Stop: "end"}
		var text strings.Builder
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "tool_use":
				if b.Name == structuredTool && req.Schema != nil {
					r.Text = string(rawObject(b.Input))
					continue
				}
				r.ToolCalls = append(r.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: rawObject(b.Input)})
			}
		}
		if r.Text == "" {
			r.Text = text.String()
		}
		switch {
		case len(r.ToolCalls) > 0:
			r.Stop = "tool"
		case stopReason == "max_tokens":
			r.Stop = "length"
		}
		return r
	}
	if stream == nil {
		var r struct {
			Model      string  `json:"model"`
			StopReason string  `json:"stop_reason"`
			Content    []block `json:"content"`
			Usage      struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return Response{}, fmt.Errorf("llm: anthropic: decode reply: %w", err)
		}
		return finish(r.Model, r.StopReason, r.Content, r.Usage.Input, r.Usage.Output), nil
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
			}
		case "content_block_stop":
			if ev.Index < len(blocks) && blocks[ev.Index].Type == "tool_use" {
				blocks[ev.Index].Input = json.RawMessage(partial[ev.Index].String())
			}
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
	return nil, fmt.Errorf("llm: anthropic has no embedding model; set LLM_PROVIDER to openai, google or ollama for Embed")
}
