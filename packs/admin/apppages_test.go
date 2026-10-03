package admin

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agim/lidza"
)

// A form returns to the view it was sent from, never to another address.
func TestAppPageBack(t *testing.T) {
	tmpl := fstest.MapFS{"posts.html": {Data: []byte(`{{define "content"}}<form method="post" action="{{.Path}}/posts/publish"><input type="hidden" name="back" value="{{$.Back}}"></form>{{end}}`)}}
	pages := []Page{{Name: "Posts", Path: "posts", Template: "posts.html",
		Actions: map[string]Action{"publish": func(*http.Request) (string, error) { return "published", nil }}}}
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return true }, CredentialsDir: t.TempDir(), Templates: tmpl, Pages: pages}, lidza.NewServices())
	if _, body := get(t, srv, "/admin/posts?post=7&saved=old"); !strings.Contains(body, `value="?post=7"`) {
		t.Fatalf("back field: %s", body)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for back, want := range map[string]string{
		"?post=7":                       "/admin/posts?post=7&saved=published",
		"?post=7&error=old":             "/admin/posts?post=7&saved=published",
		"//elsewhere.example/x":         "/admin/posts?saved=published",
		"https://elsewhere.example/?a=": "/admin/posts?saved=published",
		"?" + strings.Repeat("a", 3000): "/admin/posts?saved=published",
		"":                              "/admin/posts?saved=published",
	} {
		res, err := client.PostForm(srv.URL+"/admin/posts/publish", url.Values{"back": {back}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if loc := res.Header.Get("Location"); loc != want {
			t.Errorf("back %.30q: %q, want %q", back, loc, want)
		}
	}
}

// Files reach an action only on a page that takes them, within its limit;
// downloads sit behind the gate.
func TestAppPageUploadsAndDownloads(t *testing.T) {
	tmpl := fstest.MapFS{"files.html": {Data: []byte(`{{define "content"}}files{{end}}`)}}
	got := ""
	upload := func(r *http.Request) (string, error) {
		f, _, err := r.FormFile("file")
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		got = string(b) + "|" + r.FormValue("note")
		return "stored", nil
	}
	pages := []Page{
		{Name: "Files", Path: "files", Template: "files.html", MaxUpload: 1 << 10, Actions: map[string]Action{"upload": upload},
			Downloads: map[string]http.HandlerFunc{"export": func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/csv")
				_, _ = io.WriteString(w, "id\n1\n")
			}}},
		{Name: "Plain", Path: "plain", Template: "files.html", Actions: map[string]Action{"upload": upload}},
	}
	allow := true
	srv := serve(t, Options{Auth: noAuth, Allow: func(context.Context) bool { return allow }, CredentialsDir: t.TempDir(), Templates: tmpl, Pages: pages}, lidza.NewServices())
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(path string, size int) string {
		t.Helper()
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		_ = w.WriteField("note", "hello")
		fw, _ := w.CreateFormFile("file", "a.txt")
		_, _ = fw.Write(bytes.Repeat([]byte("x"), size))
		_ = w.Close()
		res, err := client.Post(srv.URL+path, w.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	if loc := post("/admin/files/upload", 3); loc != "/admin/files?saved=stored" || got != "xxx|hello" {
		t.Fatalf("upload: %q %q", loc, got)
	}
	got = ""
	if loc := post("/admin/files/upload", 4<<10); !strings.Contains(loc, "error=the+upload+is+larger+than") || got != "" {
		t.Fatalf("too large: %q %q", loc, got)
	}
	if loc := post("/admin/plain/upload", 3); !strings.Contains(loc, "error=this+form+takes+no+files") || got != "" {
		t.Fatalf("file on a page without uploads: %q %q", loc, got)
	}
	if code, body := get(t, srv, "/admin/files/export"); code != 200 || body != "id\n1\n" {
		t.Fatalf("download: %d %q", code, body)
	}
	allow = false
	if code, body := get(t, srv, "/admin/files/export"); code == 200 || strings.Contains(body, "id\n1") {
		t.Fatalf("download past the gate: %d", code)
	}
	defer func() {
		if recover() == nil {
			t.Error("a download with a nested name was accepted")
		}
	}()
	New(Options{Pages: []Page{{Name: "X", Path: "x", Template: "x.html", Downloads: map[string]http.HandlerFunc{"a/b": func(http.ResponseWriter, *http.Request) {}}}}})
}
