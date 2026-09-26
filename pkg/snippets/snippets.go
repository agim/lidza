// Package snippets embeds the files of the reference app (examples/notes)
// that show how the common tasks are done: an owned resource, auth routes,
// a page on the generated client, a handler test, a browser test, an MCP
// tool. `lidza mcp` serves them as lidza_snippet and `lidza snippet`
// prints them, so an agent copies code that compiles and passes its tests
// instead of guessing. `go generate ./pkg/snippets` refreshes the copies in _files (the
// underscore keeps the go tool from building them as packages).
package snippets

import (
	"embed"
	"strings"
)

//go:generate go run ./internal/sync

//go:embed all:_files
var files embed.FS

// Snippet is one file of the reference app.
type Snippet struct {
	// Name is the key an agent asks for.
	Name string `json:"name"`
	// File is the path in examples/notes.
	File        string `json:"file"`
	Description string `json:"description"`
}

// Index lists the snippets in reading order.
var Index = []Snippet{
	{"schema", "schema.lidza", "Models, API types with rules, optional fields, and the types the auth routes reply with."},
	{"routes", "routes.go", "Public routes; a group behind auth.Optional() for a page that serves visitors too; groups behind auth.Require(); a resource mounted on a group."},
	{"auth-handlers", "handlers/auth.go", "Register, login, logout, email verification and password reset, and the session route read from the database: password hashing, HttpOnly cookies plus a bearer token (the auth pack renews them), normalized emails, links through mail.Link, 409 on a taken email, 401 on a wrong password."},
	{"start", "start.go", "The app's start hook (onStart, wired in main.go): where job handlers are registered with jobs.FromServices(s).Handle and services are provided."},
	{"resource-handlers", "handlers/note.go", "The handlers `lidza gen resource` writes, scoped to the signed-in user: list, get, create, patch, delete with 404 for another user's rows."},
	{"queries", "db/queries/note.sql", "sqlc queries for an owned resource: owner checks on every statement, COALESCE with sqlc.narg for a partial update, :execrows for delete."},
	{"handler-test", "routes_test.go", "A Go test with lidzatest.Start: cookies carried across calls, a bearer token, mail read from the outbox with WaitFor and srv.Context(), and the expected 401, 409, 422 and 404 replies."},
	{"mcp-tool", "tools.go", "An app MCP tool (lidza.ToolFunc) that queries the database through the db pack."},
	{"mail-template", "mail/verify.txt.tmpl", "A mail template (Go text/template over the Data of mail.Message); the html variant sits next to it. handlers/auth.go sends it through the mail pack."},
	{"storage-handler", "handlers/attachment.go", "Raw upload and download handlers through the storage pack: the body stored under a key derived from the row, the same access check as the row, streaming back through the app, the object deleted with the row."},
	{"admin-page", "handlers/admin.go", "An app page in the admin pages (admin.Page): its data from sqlc queries across accounts, a delete action returning the message the page shows, mounted in routes.go with its template embedded."},
	{"admin-page-template", "admin/notes.html", "The admin page's template: it defines \"content\" inside the admin frame, with Tabler's card and table classes, since and icon, an empty state through admin-empty, and a form posting to the page's action with a confirmation."},
	{"llm-handler", "handlers/tags.go", "A route that asks the language model for a schema type through the llm pack: llm.Generate over the note the user may read, the prompt in the handler, the reply validated by the type's rules."},
	{"pack-capability", "packs/stats/rust/src/lib.rs", "A Rust pack capability (lidza_export!) over types from schema.lidza: one CPU pass over user text, no I/O; the handler noteStats in resource-handlers calls it through the generated wrapper."},
	{"pack-manifest", "packs/stats/pack.lidza.json", "The pack manifest: capability names with their input and output types, the pool size, memory cap, timeout, and uninterruptible for input-bounded loops."},
	{"page", "src/pages/Home.tsx", "A React page on @lidza/client only: useQuery and useMutation with api.*, validators.* before a request, labelled inputs, sign in and sign out."},
	{"browser-test", "e2e/notes.spec.ts", "A Playwright test: register, add a note, reload, sign out, and no console or window errors."},
}

// Names lists the snippet names.
func Names() []string {
	out := make([]string, len(Index))
	for i, s := range Index {
		out[i] = s.Name
	}
	return out
}

// Get returns a snippet's description and content.
func Get(name string) (Snippet, string, bool) {
	for _, s := range Index {
		if s.Name == name {
			data, err := files.ReadFile("_files/" + s.File)
			if err != nil {
				return s, "", false
			}
			return s, string(data), true
		}
	}
	return Snippet{}, "", false
}

// Header is the line that opens every snippet when printed: where it
// comes from and that it is verified code.
func Header(s Snippet) string {
	return "From examples/notes/" + s.File + " of Līdza (verified by its tests). " + s.Description
}

// Catalog renders the index as text.
func Catalog() string {
	var b strings.Builder
	for _, s := range Index {
		b.WriteString(s.Name + " (" + s.File + "): " + s.Description + "\n")
	}
	return b.String()
}
