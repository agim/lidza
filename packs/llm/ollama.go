package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// ollama speaks the Ollama API: POST /api/chat (NDJSON when streaming)
// and POST /api/embed. Tool calls carry no ids; the pack numbers them.
type ollama struct{ base, model, embedModel string }

func (p *ollama) Name() string { return "ollama" }

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func (p *ollama) Chat(ctx context.Context, req Request, stream func(string) error) (Response, error) {
	msgs := []ollamaMessage{}
	if req.System != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		om := ollamaMessage{Role: string(m.Role), Content: m.Content}
		for _, c := range m.ToolCalls {
			var tc ollamaToolCall
			tc.Function.Name = c.Name
			tc.Function.Arguments = rawObject(c.Input)
			om.ToolCalls = append(om.ToolCalls, tc)
		}
		msgs = append(msgs, om)
	}
	body := map[string]any{
		"model":    or(req.Model, p.model),
		"messages": msgs,
		"stream":   stream != nil,
		"options":  map[string]any{"num_predict": req.MaxTokens},
	}
	if req.Temperature != nil {
		body["options"].(map[string]any)["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		body["tools"] = tools
	}
	if req.Schema != nil {
		body["format"] = req.Schema
	}
	type reply struct {
		Model           string        `json:"model"`
		Message         ollamaMessage `json:"message"`
		Done            bool          `json:"done"`
		DoneReason      string        `json:"done_reason"`
		PromptEvalCount int           `json:"prompt_eval_count"`
		EvalCount       int           `json:"eval_count"`
		Error           string        `json:"error"`
	}
	finish := func(r reply, text string) Response {
		res := Response{Text: text, Model: r.Model, Usage: Usage{Input: r.PromptEvalCount, Output: r.EvalCount}, Stop: "end"}
		for i, c := range r.Message.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: callID(i + 1), Name: c.Function.Name, Input: rawObject(c.Function.Arguments)})
		}
		if len(res.ToolCalls) > 0 {
			res.Stop = "tool"
		} else if r.DoneReason == "length" {
			res.Stop = "length"
		}
		return res
	}
	res, err := send(ctx, "ollama", p.base+"/api/chat", nil, body)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	if stream == nil {
		var r reply
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			return Response{}, fmt.Errorf("llm: ollama: decode reply: %w", err)
		}
		if r.Error != "" {
			return Response{}, &Error{Provider: "ollama", Status: 400, Body: r.Error}
		}
		return finish(r, r.Message.Content), nil
	}
	var last reply
	var text string
	err = readLines(res.Body, func(line []byte) error {
		var r reply
		if err := json.Unmarshal(line, &r); err != nil {
			return fmt.Errorf("llm: ollama: decode stream: %w", err)
		}
		if r.Error != "" {
			return &Error{Provider: "ollama", Status: 400, Body: r.Error}
		}
		if r.Message.Content != "" {
			text += r.Message.Content
			if err := stream(r.Message.Content); err != nil {
				return err
			}
		}
		if len(r.Message.ToolCalls) > 0 {
			last.Message.ToolCalls = append(last.Message.ToolCalls, r.Message.ToolCalls...)
		}
		if r.Done {
			r.Message.ToolCalls = last.Message.ToolCalls
			last = r
		}
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	return finish(last, text), nil
}

func (p *ollama) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		Error      string      `json:"error"`
	}
	if err := postJSON(ctx, "ollama", p.base+"/api/embed", nil, map[string]any{"model": or(model, p.embedModel), "input": texts}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, &Error{Provider: "ollama", Status: 400, Body: out.Error}
	}
	return out.Embeddings, nil
}
