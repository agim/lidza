package sdk

import (
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/inspect"
)

func TestTypeScript(t *testing.T) {
	files := TypeScript(sampleContext())
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
		"summarize(body: T.CreatePost, options?: RequestOptions): AsyncGenerator<T.SummarizeEvent, void, undefined> {",
		"return stream<T.SummarizeEvent>(\"POST\", `/api/v1/summarize`, body, options)",
		"feed(params: { id: string }, options?: RequestOptions): AsyncGenerator<T.Post, void, undefined> {",
		"async function* stream<E>(",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.ts missing %q\n%s", want, client)
		}
	}
	if !strings.Contains(files["index.ts"], "export * from './client'") || !strings.Contains(files["index.ts"], "export * from './validators'") {
		t.Error("index.ts")
	}
	validators := files["validators.ts"]
	for _, want := range []string{
		"export function validateCreatePost(v: T.CreatePost): FieldError[]",
		`if (v.title === '') errs.push({ field: "title", rule: "required", message: "required" })`,
		`if (v.title.length < 1) errs.push({ field: "title", rule: "min", message: "at least 1 character(s)" })`,
		"  CreatePost: validateCreatePost,",
	} {
		if !strings.Contains(validators, want) {
			t.Errorf("validators.ts missing %q\n%s", want, validators)
		}
	}
	if strings.Contains(validators, "validateHealth") {
		t.Error("types without rules should not get validators")
	}
	if strings.Contains(validators, "const EMAIL") || strings.Contains(validators, "function isURL") {
		t.Error("unused helpers emitted")
	}
}

// TestTypeScriptUnused: an API without {name} path parameters or schema
// rules gets neither the encoder nor the types import (noUnusedLocals).
func TestTypeScriptUnused(t *testing.T) {
	files := TypeScript(&inspect.Context{Operations: []inspect.Operation{
		{ID: "ping", Method: "POST", Path: "/api/v1/ping", Params: []string{}},
		{ID: "getFiles", Method: "GET", Path: "/api/v1/files/{path...}", Params: []string{"path"}},
	}})
	if strings.Contains(files["client.ts"], "const p =") {
		t.Error("client.ts declares p without a {name} parameter")
	}
	if strings.Contains(files["validators.ts"], "import type") {
		t.Error("validators.ts imports the types without a validator")
	}
	if strings.Contains(files["client.ts"], "function* stream") {
		t.Error("client.ts declares stream without a streamed operation")
	}
	if !strings.Contains(TypeScript(sampleContext())["client.ts"], "const p = encodeURIComponent") {
		t.Error("client.ts lacks p with a {name} parameter")
	}
}

// sampleContext is an API with path parameters, bodies, enums, nullable
// and optional fields, and schema rules.
func sampleContext() *inspect.Context {
	return &inspect.Context{
		Operations: []inspect.Operation{
			{ID: "health", Method: "GET", Path: "/api/v1/health", Params: []string{}, Output: "Health", Builtin: true},
			{ID: "getPost", Method: "GET", Path: "/api/v1/posts/{id}", Params: []string{"id"}, Output: "Post"},
			{ID: "createPost", Method: "POST", Path: "/api/v1/posts", Params: []string{}, Input: "CreatePost", Output: "Post"},
			{ID: "deletePost", Method: "DELETE", Path: "/api/v1/posts/{id}", Params: []string{"id"}},
			{ID: "getFiles", Method: "GET", Path: "/api/v1/files/{path...}", Params: []string{"path"}, Output: "Post"},
			{ID: "summarize", Method: "POST", Path: "/api/v1/summarize", Params: []string{}, Input: "CreatePost", Output: "SummarizeEvent", Stream: true},
			{ID: "feed", Method: "GET", Path: "/api/v1/posts/{id}/feed", Params: []string{"id"}, Output: "Post", Stream: true},
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
			"User":           map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}},
			"CreatePost":     map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string", "minLength": 1}}, "required": []string{"title"}, "x-lidza": "schema"},
			"SummarizeEvent": map[string]any{"type": "string"},
		},
	}
}
