package sdk

import (
	"strings"
	"testing"

	"github.com/agim/lidza/pkg/inspect"
)

func TestDart(t *testing.T) {
	c := &inspect.Context{
		App: inspect.App{Name: "demo"},
		Operations: []inspect.Operation{
			{ID: "getPost", Method: "GET", Path: "/api/v1/posts/{id}", Params: []string{"id"}, Output: "Post"},
			{ID: "createPost", Method: "POST", Path: "/api/v1/posts", Params: []string{}, Input: "CreatePost", Output: "Post"},
			{ID: "deletePost", Method: "DELETE", Path: "/api/v1/posts/{id}", Params: []string{"id"}},
		},
		Schemas: map[string]any{
			"Status": map[string]any{"type": "string", "enum": []string{"draft", "in-review"}},
			"Post": map[string]any{"type": "object", "properties": map[string]any{
				"id":        map[string]any{"type": "string", "format": "uuid"},
				"body":      map[string]any{"type": []any{"string", "null"}},
				"status":    map[string]any{"$ref": "#/components/schemas/Status"},
				"tags":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"views":     map[string]any{"type": "integer"},
				"score":     map[string]any{"type": "number"},
				"createdAt": map[string]any{"type": "string", "format": "date-time"},
				"counts":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}},
				"author":    map[string]any{"oneOf": []any{map[string]any{"$ref": "#/components/schemas/User"}, map[string]any{"type": "null"}}},
				"class":     map[string]any{"type": "string"},
			}, "required": []string{"id", "body", "status", "tags", "views", "score", "createdAt", "counts", "author", "class"}},
			"User":       map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}},
			"CreatePost": map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}, "required": []string{"title"}},
		},
	}
	files := Dart(c)
	types := files["lib/src/types.dart"]
	for _, want := range []string{
		"enum Status {\n  draft('draft'),\n  inReview('in-review');",
		"class Post {",
		"  final String id;",
		"  final String? body;",
		"  final Status status;",
		"  final List<String> tags;",
		"  final int views;",
		"  final double score;",
		"  final DateTime createdAt;",
		"  final Map<String, int> counts;",
		"  final User? author;",
		"  final String class_;",
		"        status: Status.fromJson(json['status'] as String),",
		"        createdAt: DateTime.parse(json['createdAt'] as String),",
		"        score: (json['score'] as num).toDouble(),",
		"        author: json['author'] == null ? null : User.fromJson(json['author'] as Map<String, dynamic>),",
		"        tags: (json['tags'] as List<dynamic>).map((e) => e as String).toList(),",
		"        'createdAt': createdAt.toUtc().toIso8601String(),",
		"        'author': author?.toJson(),",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.dart missing %q\n%s", want, types)
		}
	}
	client := files["lib/src/client.dart"]
	for _, want := range []string{
		"class LidzaClient {",
		"Future<Post> getPost({required String id}) async {",
		"await _send('GET', '/api/v1/posts/${Uri.encodeComponent(id)}');",
		"Future<Post> createPost(CreatePost body) async {",
		"body: body.toJson()",
		"Future<void> deletePost({required String id}) async {",
		"class ApiException implements Exception",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.dart missing %q\n%s", want, client)
		}
	}
	if !strings.Contains(files["pubspec.yaml"], "name: lidza_client") || !strings.Contains(files["lib/lidza_client.dart"], "export 'src/client.dart';") {
		t.Error("package files")
	}
	if dartIdent("created_at") != "createdAt" || dartIdent("in") != "in_" || dartIdent("2fa") != "v2fa" {
		t.Error("dartIdent")
	}
}
