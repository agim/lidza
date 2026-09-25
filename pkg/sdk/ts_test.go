package sdk

import (
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/inspect"
)

func TestTypeScript(t *testing.T) {
	c := &inspect.Context{
		Operations: []inspect.Operation{
			{ID: "health", Method: "GET", Path: "/api/v1/health", Params: []string{}, Output: "Health", Builtin: true},
			{ID: "getPost", Method: "GET", Path: "/api/v1/posts/{id}", Params: []string{"id"}, Output: "Post"},
			{ID: "createPost", Method: "POST", Path: "/api/v1/posts", Params: []string{}, Input: "CreatePost", Output: "Post"},
			{ID: "deletePost", Method: "DELETE", Path: "/api/v1/posts/{id}", Params: []string{"id"}},
			{ID: "getFiles", Method: "GET", Path: "/api/v1/files/{path...}", Params: []string{"path"}, Output: "Post"},
		},
		Schemas: map[string]any{
			"Health": map[string]any{"type": "object", "properties": map[string]any{
				"status": map[string]any{"type": "string"}, "time": map[string]any{"type": "string", "format": "date-time"},
			}, "required": []string{"status", "time"}},
			"Status": map[string]any{"type": "string", "enum": []string{"draft", "published"}},
			"Post": map[string]any{"type": "object", "properties": map[string]any{
				"id":     map[string]any{"type": "string", "format": "uuid"},
				"body":   map[string]any{"type": []any{"string", "null"}},
				"status": map[string]any{"$ref": "#/components/schemas/Status"},
				"tags":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"meta":   map[string]any{},
				"extra":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}},
				"author": map[string]any{"oneOf": []any{map[string]any{"$ref": "#/components/schemas/User"}, map[string]any{"type": "null"}}},
				"opt":    map[string]any{"type": "integer"},
			}, "required": []string{"id", "body", "status", "tags", "meta", "extra", "author"}},
			"User":       map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}},
			"CreatePost": map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "minLength": 1}}, "required": []string{"title"}},
		},
	}
	files := TypeScript(c)
	types := files["types.ts"]
	for _, want := range []string{
		`export type Status = "draft" | "published"`,
		"export interface Post {",
		"  body: string | null",
		"  status: Status",
		"  tags: string[]",
		"  meta: unknown",
		"  extra: Record<string, number>",
		"  author: User | null",
		"  opt?: number",
		"export interface CreatePost {",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.ts missing %q\n%s", want, types)
		}
	}
	client := files["client.ts"]
	for _, want := range []string{
		"health(options?: RequestOptions): Promise<T.Health>",
		"getPost(params: { id: string }, options?: RequestOptions): Promise<T.Post>",
		"return request<T.Post>(\"GET\", `/api/v1/posts/${p(params.id)}`, undefined, options)",
		"createPost(body: T.CreatePost, options?: RequestOptions): Promise<T.Post>",
		"return request<T.Post>(\"POST\", `/api/v1/posts`, body, options)",
		"deletePost(params: { id: string }, options?: RequestOptions): Promise<void>",
		"`/api/v1/files/${params.path}`",
		"export class ApiError extends Error",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.ts missing %q\n%s", want, client)
		}
	}
	if !strings.Contains(files["index.ts"], "export * from './client'") {
		t.Error("index.ts")
	}
}
