package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBucket is an S3 stub with an anonymous-access policy per key: a
// signed request is the owner; an unsigned one gets what the policy says.
type fakeBucket struct {
	mu      sync.Mutex
	objects map[string]string
	public  func(key string) bool // anonymous GET/HEAD allowed
	listing bool                  // anonymous listing allowed
	special map[string]func(w http.ResponseWriter)
	anon    []*http.Request
}

func (f *fakeBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	signed := r.Header.Get("Authorization") != ""
	if !signed {
		f.anon = append(f.anon, r.Clone(context.Background()))
		if h, ok := f.special[key]; ok {
			h(w)
			return
		}
	}
	if r.URL.Path == "/bucket" || r.URL.Path == "/bucket/" {
		if !signed && !f.listing {
			http.Error(w, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
			return
		}
		w.Write([]byte(`<?xml version="1.0"?><ListBucketResult></ListBucketResult>`))
		return
	}
	_, exists := f.objects[key]
	if !signed && !f.public(key) {
		http.Error(w, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[key] = string(b)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(204)
	default:
		if !exists {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", "10")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
	}
}

func privacyStore(t *testing.T, f *fakeBucket, public string) *Storage {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s, err := New(Config{Provider: "s3", Bucket: "bucket", AccessKey: "AKIDSECRETID", SecretKey: "very-secret-key", Endpoint: srv.URL, PublicURL: public, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func outcomes(r PrivacyReport) string {
	var out []string
	for _, c := range r.Checks {
		out = append(out, c.Check+"="+c.Outcome)
	}
	return strings.Join(out, ",")
}

func TestPrivacy(t *testing.T) {
	ctx := context.Background()
	f := &fakeBucket{
		objects: map[string]string{"backups/db.dump": "dump", "media/logo.png": "png", "backups/notes.txt": "x", "slow": "x", "moved": "x"},
		// media/ is public, and so are .dump files (a careless policy).
		public: func(key string) bool { return strings.HasPrefix(key, "media/") || strings.HasSuffix(key, ".dump") },
		special: map[string]func(http.ResponseWriter){
			"moved": func(w http.ResponseWriter) {
				w.Header().Set("Location", "https://elsewhere.example/x?X-Amz-Signature=leak")
				w.WriteHeader(301)
			},
			"slow": func(w http.ResponseWriter) { time.Sleep(time.Second) },
		},
	}
	s := privacyStore(t, f, "")

	// A private object beside public media: verified private.
	r, err := s.Privacy(ctx, "backups/notes.txt")
	if err != nil || !r.Private || r.Public || outcomes(r) != "object=denied,listing=denied" || r.At.IsZero() {
		t.Fatalf("private object: %s %+v %v", outcomes(r), r, err)
	}
	// A public object in a bucket that refuses listing: public.
	if r, _ := s.Privacy(ctx, "media/logo.png"); !r.Public || r.Private || outcomes(r) != "object=public,listing=denied" {
		t.Fatalf("public object: %s", outcomes(r))
	}
	// A policy exposing .dump files: the backup is public though a
	// probe with another name would say private.
	if r, _ := s.Privacy(ctx, "backups/db.dump"); !r.Public {
		t.Fatalf(".dump policy: %s", outcomes(r))
	}
	probe, err := s.PrivacyProbe(ctx, "backups/probe.txt")
	if err != nil || !probe.Probe || !probe.Private {
		t.Fatalf("probe .txt: %s %v", outcomes(probe), err)
	}
	if probe, _ := s.PrivacyProbe(ctx, "backups/probe.dump"); !probe.Public || !probe.Probe {
		t.Fatalf("probe .dump: %s", outcomes(probe))
	}
	if _, ok := f.objects["backups/probe.txt"]; ok {
		t.Fatal("the probe was left behind")
	}
	if _, err := s.PrivacyProbe(ctx, "backups/notes.txt"); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("probe over an existing object: %v", err)
	}
	// A missing object is an error, not "private".
	if _, err := s.Privacy(ctx, "backups/missing.dump"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing: %v", err)
	}
	// A redirect is inconclusive, never followed, its query redacted.
	r, _ = s.Privacy(ctx, "moved")
	if r.Private || r.Public || r.Checks[0].Outcome != Inconclusive || strings.Contains(r.Checks[0].Detail, "Signature") {
		t.Fatalf("redirect: %+v", r.Checks[0])
	}
	// A timeout is inconclusive.
	if r, _ := s.Privacy(ctx, "slow"); r.Private || r.Checks[0].Outcome != Inconclusive {
		t.Fatalf("timeout: %+v", r.Checks[0])
	}
	// No anonymous request carried credentials, a signature or cookies.
	for _, a := range f.anon {
		if a.Header.Get("Authorization") != "" || a.Header.Get("Cookie") != "" || strings.Contains(a.URL.RawQuery, "X-Amz") || a.Header.Get("X-Amz-Content-Sha256") != "" {
			t.Fatalf("an anonymous request carried credentials: %s %v", a.URL, a.Header)
		}
	}
	for _, c := range r.Checks {
		if strings.Contains(c.Scope+c.Detail, "very-secret-key") || strings.Contains(c.Scope+c.Detail, "AKIDSECRETID") {
			t.Fatalf("credentials in the report: %+v", c)
		}
	}
}

// TestPrivacyCDN: the origin refuses, the CDN still serves a copy it
// cached when the object was public: the report is public.
func TestPrivacyCDN(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("credentials sent to the CDN")
		}
		w.WriteHeader(200)
	}))
	defer cdn.Close()
	f := &fakeBucket{objects: map[string]string{"backups/db.dump": "x"}, public: func(string) bool { return false }}
	s := privacyStore(t, f, cdn.URL)
	r, err := s.Privacy(context.Background(), "backups/db.dump")
	if err != nil || !r.Public || r.Private || outcomes(r) != "object=denied,listing=denied,public-url=public" {
		t.Fatalf("cdn: %s %v", outcomes(r), err)
	}
	local, _ := New(Config{Dir: t.TempDir()})
	if _, err := local.Privacy(context.Background(), "x"); err != ErrPrivacyUnsupported {
		t.Fatalf("local: %v", err)
	}
}
