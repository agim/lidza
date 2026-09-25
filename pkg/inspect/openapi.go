package inspect

import (
	"strconv"
	"strings"
)

// OpenAPI renders the OpenAPI 3.1 document for the project: one operation
// per typed route with its schemas, and a bare entry for each untyped
// route. Path parameters are strings.
func OpenAPI(c *Context) map[string]any {
	paths := map[string]any{}
	item := func(path string) map[string]any {
		if p, ok := paths[path].(map[string]any); ok {
			return p
		}
		p := map[string]any{}
		paths[path] = p
		return p
	}
	typed := map[string]bool{}
	for _, op := range c.Operations {
		typed[op.Method+" "+op.Path] = true
		o := map[string]any{"operationId": op.ID}
		if op.Builtin {
			o["tags"] = []string{"lidza"}
		}
		if len(op.Params) > 0 {
			var params []any
			for _, p := range op.Params {
				params = append(params, map[string]any{"name": p, "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
			}
			o["parameters"] = params
		}
		if op.Input != "" {
			o["requestBody"] = map[string]any{"required": true, "content": jsonContent(op.Input)}
		}
		responses := map[string]any{}
		if op.Output != "" {
			responses["200"] = map[string]any{"description": "OK", "content": jsonContent(op.Output)}
		} else {
			responses["204"] = map[string]any{"description": "No Content"}
		}
		if op.Input != "" {
			responses["400"] = map[string]any{"description": "Malformed body", "content": errorContent()}
			responses["422"] = map[string]any{"description": "Validation failed", "content": map[string]any{"application/json": map[string]any{"schema": ref("ValidationError")}}}
		}
		responses["default"] = map[string]any{"description": "Error", "content": errorContent()}
		o["responses"] = responses
		item(op.Path)[method(op.Method)] = o
	}
	for _, r := range c.Routes {
		if typed[r.Method+" "+r.Path] {
			continue
		}
		o := map[string]any{
			"operationId": operationID(r.Handler.Name, r.Method, r.Path),
			"responses":   map[string]any{"default": map[string]any{"description": "Response"}},
		}
		if len(pathParams(r.Path)) > 0 {
			var params []any
			for _, p := range pathParams(r.Path) {
				params = append(params, map[string]any{"name": p, "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
			}
			o["parameters"] = params
		}
		item(r.Path)[method(r.Method)] = o
	}

	schemas := map[string]any{}
	for k, v := range c.Schemas {
		schemas[k] = v
	}
	schemas["Error"] = map[string]any{"type": "object", "properties": map[string]any{"error": map[string]any{"type": "string"}}, "required": []string{"error"}}
	schemas["ValidationError"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error": map[string]any{"type": "string", "const": "validation"},
			"fields": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field":   map[string]any{"type": "string"},
					"rule":    map[string]any{"type": "string"},
					"message": map[string]any{"type": "string"},
				},
				"required": []string{"field", "rule", "message"},
			}},
		},
		"required": []string{"error", "fields"},
	}
	return map[string]any{
		"openapi":    "3.1.0",
		"info":       map[string]any{"title": c.App.Name, "version": strings.TrimPrefix(c.Lidza, "v")},
		"paths":      paths,
		"components": map[string]any{"schemas": schemas},
	}
}

func method(m string) string {
	if m == "" {
		return "get"
	}
	return strings.ToLower(m)
}

func jsonContent(component string) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": ref(component)}}
}

func errorContent() map[string]any { return jsonContent("Error") }

// strconvUnquote is strconv.Unquote, named for the one call site in types.go.
func strconvUnquote(s string) (string, error) { return strconv.Unquote(s) }
