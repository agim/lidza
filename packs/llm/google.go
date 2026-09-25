package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// google speaks the Gemini API: POST models/{model}:generateContent
// (:streamGenerateContent?alt=sse when streaming) and
// models/{model}:batchEmbedContents. A Schema goes in
// generationConfig.responseSchema; function calls carry no ids.
type google struct{ key, base, model, embedModel string }

func (p *google) Name() string { return "google" }

func (p *google) headers() map[string]string {
	return map[string]string{"x-goog-api-key": p.key}
}

type googlePart struct {
	Text         string `json:"text,omitempty"`
	FunctionCall *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall,omitempty"`
	FunctionResponse *struct {
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse,omitempty"`
}

func (p *google) Chat(ctx context.Context, req Request, stream func(string) error) (Response, error) {
	var contents []map[string]any
	for _, m := range req.Messages {
		switch m.Role {
		case ToolResult:
			var response json.RawMessage
			if json.Valid([]byte(m.Content)) && strings.HasPrefix(strings.TrimSpace(m.Content), "{") {
				response = json.RawMessage(m.Content)
			} else {
				response, _ = json.Marshal(map[string]string{"result": m.Content})
			}
			part := map[string]any{"functionResponse": map[string]any{"name": m.Name, "response": response}}
			// Consecutive results share one user turn.
			if n := len(contents); n > 0 && contents[n-1]["role"] == "user" {
				if parts, ok := contents[n-1]["parts"].([]map[string]any); ok && len(parts) > 0 && parts[0]["functionResponse"] != nil {
					contents[n-1]["parts"] = append(parts, part)
					continue
				}
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{part}})
		case Assistant:
			var parts []map[string]any
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, c := range m.ToolCalls {
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": c.Name, "args": rawObject(c.Input)}})
			}
			contents = append(contents, map[string]any{"role": "model", "parts": parts})
		default:
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{{"text": m.Content}}})
		}
	}
	gen := map[string]any{"maxOutputTokens": req.MaxTokens}
	if req.Temperature != nil {
		gen["temperature"] = *req.Temperature
	}
	if req.Schema != nil {
		gen["responseMimeType"] = "application/json"
		gen["responseSchema"] = googleSchema(req.Schema)
	}
	body := map[string]any{"contents": contents, "generationConfig": gen}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": req.System}}}
	}
	if len(req.Tools) > 0 {
		var decls []map[string]any
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": googleSchema(t.Schema)})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	model := or(req.Model, p.model)
	url := p.base + "/v1beta/models/" + model + ":generateContent"
	if stream != nil {
		url = p.base + "/v1beta/models/" + model + ":streamGenerateContent?alt=sse"
	}
	res, err := send(ctx, "google", url, p.headers(), body)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()

	type reply struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []googlePart `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			Prompt     int `json:"promptTokenCount"`
			Candidates int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	r := Response{Model: model, Stop: "end"}
	var text strings.Builder
	var reason string
	absorb := func(rp reply, emit bool) error {
		if rp.ModelVersion != "" {
			r.Model = rp.ModelVersion
		}
		if rp.UsageMetadata.Prompt > 0 || rp.UsageMetadata.Candidates > 0 {
			r.Usage = Usage{Input: rp.UsageMetadata.Prompt, Output: rp.UsageMetadata.Candidates}
		}
		for _, c := range rp.Candidates {
			if c.FinishReason != "" {
				reason = c.FinishReason
			}
			for _, part := range c.Content.Parts {
				if part.Text != "" {
					text.WriteString(part.Text)
					if emit {
						if err := stream(part.Text); err != nil {
							return err
						}
					}
				}
				if part.FunctionCall != nil {
					r.ToolCalls = append(r.ToolCalls, ToolCall{ID: callID(len(r.ToolCalls) + 1), Name: part.FunctionCall.Name, Input: rawObject(part.FunctionCall.Args)})
				}
			}
		}
		return nil
	}
	if stream == nil {
		var rp reply
		if err := json.NewDecoder(res.Body).Decode(&rp); err != nil {
			return Response{}, fmt.Errorf("llm: google: decode reply: %w", err)
		}
		if err := absorb(rp, false); err != nil {
			return Response{}, err
		}
	} else {
		err = readSSE(res.Body, func(data string) error {
			var rp reply
			if err := json.Unmarshal([]byte(data), &rp); err != nil {
				return fmt.Errorf("llm: google: decode stream: %w", err)
			}
			return absorb(rp, true)
		})
		if err != nil {
			return Response{}, err
		}
	}
	r.Text = text.String()
	switch {
	case len(r.ToolCalls) > 0:
		r.Stop = "tool"
	case reason == "MAX_TOKENS":
		r.Stop = "length"
	}
	return r, nil
}

func (p *google) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	model = or(model, p.embedModel)
	var requests []map[string]any
	for _, t := range texts {
		requests = append(requests, map[string]any{"model": "models/" + model, "content": map[string]any{"parts": []map[string]any{{"text": t}}}})
	}
	var out struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := postJSON(ctx, "google", p.base+"/v1beta/models/"+model+":batchEmbedContents", p.headers(), map[string]any{"requests": requests}, &out); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(out.Embeddings))
	for i, e := range out.Embeddings {
		vectors[i] = e.Values
	}
	return vectors, nil
}

// googleSchema keeps the OpenAPI subset Gemini accepts: no
// additionalProperties, $schema, format on strings, or contentEncoding.
func googleSchema(s map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range s {
		switch k {
		case "additionalProperties", "$schema", "contentEncoding", "format", "title", "default":
			continue
		case "properties":
			props := map[string]any{}
			if m, ok := v.(map[string]any); ok {
				for name, sub := range m {
					if subm, ok := sub.(map[string]any); ok {
						props[name] = googleSchema(subm)
					}
				}
			}
			out[k] = props
		case "items":
			if subm, ok := v.(map[string]any); ok {
				out[k] = googleSchema(subm)
			}
		case "type":
			// A nullable field is ["string", "null"] in JSON Schema; Gemini
			// takes one type and a nullable flag.
			if list, ok := v.([]any); ok {
				for _, t := range list {
					if ts, _ := t.(string); ts == "null" {
						out["nullable"] = true
					} else if ts != "" {
						out["type"] = ts
					}
				}
				continue
			}
			out[k] = v
		default:
			out[k] = v
		}
	}
	return out
}
