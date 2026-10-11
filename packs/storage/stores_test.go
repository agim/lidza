package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agim/lidza"
)

// TestNamedStores: STORAGE_STORES declares further stores, each with its
// own bucket and prefix, taking the provider, address and keys from the
// main store unless it sets its own; one sharing the main bucket and
// prefix is refused.
func TestNamedStores(t *testing.T) {
	values := map[string]string{
		"STORAGE_PROVIDER": "spaces", "STORAGE_REGION": "ams3", "STORAGE_BUCKET": "private", "STORAGE_PREFIX": "app",
		"STORAGE_ACCESS_KEY": "ak", "STORAGE_SECRET_KEY": "sk", "STORAGE_PUBLIC_URL": "https://private.example",
		"STORAGE_CDN": "digitalocean", "STORAGE_CDN_ENDPOINT_ID": "main-endpoint", "STORAGE_CDN_TOKEN": "dop_token",
		"STORAGE_STORES":                "media, Exports",
		"STORAGE_MEDIA_BUCKET":          "public",
		"STORAGE_MEDIA_PUBLIC_URL":      "https://cdn.example.com",
		"STORAGE_MEDIA_CDN_ENDPOINT_ID": "media-endpoint",
		"STORAGE_EXPORTS_BUCKET":        "exports",
		"STORAGE_EXPORTS_ACCESS_KEY":    "ak2",
		"STORAGE_EXPORTS_SECRET_KEY":    "sk2",
		"STORAGE_EXPORTS_CDN":           "",
	}
	s, err := build(values)
	if err != nil {
		t.Fatal(err)
	}
	media, ok := s.Store("media")
	if !ok {
		t.Fatal("no media store")
	}
	c := media.now().cfg
	if c.Provider != "spaces" || c.Bucket != "public" || c.Prefix != "" || c.AccessKey != "ak" || c.CDNToken != "dop_token" || c.CDNEndpointID != "media-endpoint" {
		t.Fatalf("media: %+v", c)
	}
	if u := media.URL("sites/a/logo.png"); u != "https://cdn.example.com/sites/a/logo.png" {
		t.Fatalf("media URL: %s", u)
	}
	exports, _ := s.Store("exports")
	if c := exports.now().cfg; c.Bucket != "exports" || c.AccessKey != "ak2" || c.PublicURL != "" || exports.purger != nil {
		t.Fatalf("exports: %+v", c)
	}
	// Named from a request's context.
	svc := lidza.NewServices()
	lidza.Provide(svc, s)
	if Named(lidza.WithServices(context.Background(), svc), "media") != media {
		t.Fatal("Named")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("an undeclared store did not panic")
			}
		}()
		Named(lidza.WithServices(context.Background(), svc), "nope")
	}()

	// The main bucket and an overlapping prefix: refused.
	values["STORAGE_MEDIA_BUCKET"] = "private"
	values["STORAGE_MEDIA_PREFIX"] = "app/public"
	if _, err := build(values); err == nil || !strings.Contains(err.Error(), "keeps its files apart") {
		t.Fatalf("overlap: %v", err)
	}
	values["STORAGE_MEDIA_PREFIX"] = "public"
	if _, err := build(values); err != nil {
		t.Fatalf("same bucket, separate prefix: %v", err)
	}
	// A bucket store needs its own bucket.
	delete(values, "STORAGE_EXPORTS_BUCKET")
	if _, err := build(values); err == nil || !strings.Contains(err.Error(), "STORAGE_EXPORTS_BUCKET") {
		t.Fatalf("no bucket: %v", err)
	}
	// Local stores get a directory of their own.
	dir := t.TempDir()
	local, err := build(map[string]string{"STORAGE_DIR": filepath.Join(dir, "files"), "STORAGE_STORES": "media"})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := local.Store("media")
	if m.now().cfg.Dir != filepath.Join(dir, "files")+"-media" {
		t.Fatalf("local dir: %s", m.now().cfg.Dir)
	}
	if err := m.Purge(context.Background(), "a/"); err != nil {
		t.Fatalf("local purge: %v", err)
	}
	if _, err := build(map[string]string{"STORAGE_STORES": "bad-name"}); err == nil {
		t.Fatal("a bad store name accepted")
	}
}

// TestPurge: paths go to the DigitalOcean CDN API under the store's
// prefix, folders as wildcards, in batches of 50; a bucket without a CDN
// says what to set.
func TestPurge(t *testing.T) {
	var mu sync.Mutex
	var batches [][]string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v2/cdn/endpoints/ep-1/cache" {
			http.Error(w, `{"message":"not found"}`, 404)
			return
		}
		var body struct{ Files []string }
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		mu.Lock()
		batches = append(batches, body.Files)
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer srv.Close()
	s, err := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s", Prefix: "app",
		CDN: "digitalocean", CDNEndpointID: "ep-1", CDNToken: "dop_t", CDNAPIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Purge(ctx, "sites/a/", "logo.png", ""); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer dop_t" || len(batches) != 1 || strings.Join(batches[0], ",") != "app/sites/a/*,app/logo.png,app/*" {
		t.Fatalf("purge: %q %v", auth, batches)
	}
	batches = nil
	many := make([]string, 120)
	for i := range many {
		many[i] = "f" + string(rune('a'+i%26)) + ".png"
	}
	if err := s.Purge(ctx, many...); err != nil || len(batches) != 3 || len(batches[2]) != 20 {
		t.Fatalf("batches: %d %v", len(batches), err)
	}
	if err := s.Purge(ctx, "../etc/"); err == nil {
		t.Fatal("an escaping prefix purged")
	}
	bad, _ := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s", CDN: "digitalocean", CDNEndpointID: "nope", CDNToken: "t", CDNAPIURL: srv.URL})
	if err := bad.Purge(ctx, "x"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("API error: %v", err)
	}
	plain, _ := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err := plain.Purge(ctx, "x"); err == nil || !strings.Contains(err.Error(), "STORAGE_CDN") {
		t.Fatalf("no CDN: %v", err)
	}
	if _, err := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s", CDN: "digitalocean"}); err == nil {
		t.Fatal("a CDN without endpoint and token accepted")
	}
}

// TestPresignDelete: a signed DELETE for buckets; the local provider
// says to call Delete.
func TestPresignDelete(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(204)
	}))
	defer srv.Close()
	s, _ := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s", Endpoint: srv.URL})
	ctx := context.Background()
	del, err := s.PresignDelete(ctx, "old.zip", time.Minute)
	get, _ := s.PresignGet(ctx, "old.zip", time.Minute)
	if err != nil || !strings.Contains(del, "X-Amz-Signature=") || del == get {
		t.Fatalf("presign delete: %s %v", del, err)
	}
	req, _ := http.NewRequest(http.MethodDelete, del, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 204 || method != http.MethodDelete || path != "/b/old.zip" {
		t.Fatalf("using it: %v %s %s", err, method, path)
	}
	local, _ := New(Config{Dir: t.TempDir()})
	if _, err := local.PresignDelete(ctx, "x", time.Minute); err == nil || !strings.Contains(err.Error(), "Delete") {
		t.Fatalf("local: %v", err)
	}
}
