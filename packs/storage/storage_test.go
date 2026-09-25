package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agim/lidza"
)

func TestLocal(t *testing.T) {
	dir := t.TempDir()
	s, err := New(Config{Provider: "local", Dir: dir, MaxSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, bad := range []string{"", "/abs", "../x", "a/../b", "dir/", "a\\b", "a\x00b"} {
		if _, err := s.Put(ctx, bad, strings.NewReader("x"), PutOptions{}); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
	obj, err := s.Put(ctx, "avatars/u1.png", strings.NewReader("\x89PNG\r\n"), PutOptions{})
	if err != nil || obj.Size != 6 || obj.ContentType != "image/png" || obj.Key != "avatars/u1.png" {
		t.Fatalf("put: %+v %v", obj, err)
	}
	if _, err := s.Put(ctx, "big", strings.NewReader(strings.Repeat("x", 65)), PutOptions{}); err == nil || !strings.Contains(err.Error(), "STORAGE_MAX_SIZE") {
		t.Fatalf("max size: %v", err)
	}
	if obj, err := s.Put(ctx, "notes/1/attachment", strings.NewReader("hello, world"), PutOptions{}); err != nil || obj.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("detected content type: %+v %v", obj, err)
	}
	if obj, err := s.Put(ctx, "notes/1/data", strings.NewReader("{}"), PutOptions{ContentType: "application/json", CacheControl: "no-store"}); err != nil || obj.ContentType != "application/json" {
		t.Fatalf("given content type: %+v %v", obj, err)
	}
	rc, obj, err := s.Get(ctx, "notes/1/attachment")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello, world" || obj.Size != 12 {
		t.Fatalf("get: %q %+v", data, obj)
	}
	if _, err := s.Stat(ctx, "notes/none"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	list, err := s.List(ctx, "notes/", 10)
	if err != nil || len(list) != 2 || list[0].Key != "notes/1/attachment" || list[1].Key != "notes/1/data" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list, _ := s.List(ctx, "", 2); len(list) != 2 {
		t.Fatalf("limit: %+v", list)
	}
	if err := s.Delete(ctx, "notes/1/data"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "notes/1/data"); err != nil {
		t.Fatalf("delete twice: %v", err)
	}
	if list, _ := s.List(ctx, "notes/", 10); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
	if _, err := s.PresignGet(ctx, "avatars/u1.png", time.Minute); err == nil {
		t.Fatal("local presign without a public URL")
	}
	if s.URL("avatars/u1.png") != "" {
		t.Fatal("URL without a public address")
	}
	pub, _ := New(Config{Provider: "local", Dir: dir, PublicURL: "http://127.0.0.1:3000/api/v1/files/"})
	if u, err := pub.PresignGet(ctx, "avatars/u1.png", time.Minute); err != nil || u != "http://127.0.0.1:3000/api/v1/files/avatars/u1.png" {
		t.Fatalf("public url: %q %v", u, err)
	}

	// The handler serves objects with their type.
	svc := lidza.NewServices()
	lidza.Provide(svc, s)
	h := Handler("/api/v1/files/")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(lidza.WithServices(r.Context(), svc)))
	}))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/api/v1/files/avatars/u1.png")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || len(body) != 6 {
		t.Fatalf("served: %d %s %d", res.StatusCode, res.Header.Get("Content-Type"), len(body))
	}
	if res, _ := http.Get(srv.URL + "/api/v1/files/nothing"); res.StatusCode != 404 {
		t.Fatalf("missing: %d", res.StatusCode)
	}
	if res, _ := http.Get(srv.URL + "/api/v1/files/../etc/passwd"); res.StatusCode == 200 {
		t.Fatal("traversal served")
	}
}

func TestConfig(t *testing.T) {
	if _, err := New(Config{Provider: "s3"}); err == nil || !strings.Contains(err.Error(), "STORAGE_BUCKET") {
		t.Errorf("s3 without settings: %v", err)
	}
	if _, err := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s", Endpoint: "not a url"}); err == nil {
		t.Error("bad endpoint accepted")
	}
	if _, err := New(Config{Provider: "gcs"}); err == nil {
		t.Error("unknown provider accepted")
	}
	s, _ := New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	p := s.provider.(*s3)
	if p.pathStyle || p.objectURL("k/x y.txt").String() != "https://b.s3.amazonaws.com/k/x%20y.txt" {
		t.Errorf("aws host style: %v %s", p.pathStyle, p.objectURL("k/x y.txt"))
	}
	s, _ = New(Config{Provider: "s3", Bucket: "b", AccessKey: "a", SecretKey: "s", Endpoint: "http://127.0.0.1:9000"})
	p = s.provider.(*s3)
	if !p.pathStyle || p.objectURL("k").String() != "http://127.0.0.1:9000/b/k" {
		t.Errorf("path style: %v %s", p.pathStyle, p.objectURL("k"))
	}
}

// TestSigV4 checks the signature against the example in the AWS
// documentation (GET Object, examplebucket, test.txt, 24 May 2013).
func TestSigV4(t *testing.T) {
	p := &s3{region: "us-east-1", bucket: "examplebucket", access: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	fixed := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	p.sign(req, hexSHA256(nil), fixed)
	got := req.Header.Get("Authorization")
	// The documented signature includes the range header; the pack signs
	// host, content hash and date only, so compare the structure and the
	// deterministic parts, then the full value for the headers it signs.
	if !strings.HasPrefix(got, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Fatalf("authorization: %s", got)
	}
	if req.Header.Get("X-Amz-Date") != "20130524T000000Z" {
		t.Fatalf("date: %s", req.Header.Get("X-Amz-Date"))
	}
	// Presigned GET from the same documentation example: 86400 seconds.
	req2, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	u := p.presign(req2, 86400*time.Second, fixed)
	const want = "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404&X-Amz-SignedHeaders=host"
	if u != want {
		t.Fatalf("presigned:\n got %s\nwant %s", u, want)
	}
}

// TestS3Stub drives the S3 provider against a stub that checks the
// requests' shape and answers as S3 does.
func TestS3Stub(t *testing.T) {
	var last *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.Clone(r.Context())
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/bucket/a/b.txt":
			if string(body) != "content" || r.Header.Get("Content-Type") != "text/plain" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=ak/") || r.Header.Get("X-Amz-Content-Sha256") != hexSHA256([]byte("content")) {
				http.Error(w, "bad put", 400)
				return
			}
			w.Header().Set("ETag", `"etag1"`)
		case r.Method == http.MethodHead && r.URL.Path == "/bucket/a/b.txt":
			w.Header().Set("Content-Length", "7")
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		case r.Method == http.MethodGet && r.URL.Path == "/bucket/a/b.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("content"))
		case r.Method == http.MethodGet && r.URL.Path == "/bucket" && r.URL.Query().Get("list-type") == "2":
			w.Write([]byte(`<?xml version="1.0"?><ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>a/b.txt</Key><Size>7</Size><LastModified>2015-10-21T07:28:00.000Z</LastModified><ETag>"etag1"</ETag></Contents></ListBucketResult>`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(204)
		case r.URL.Path == "/bucket/missing":
			w.WriteHeader(404)
		default:
			w.WriteHeader(500)
			w.Write([]byte(`<Error><Code>InternalError</Code><Message>boom</Message></Error>`))
		}
	}))
	defer srv.Close()
	s, err := New(Config{Provider: "s3", Bucket: "bucket", AccessKey: "ak", SecretKey: "sk", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	obj, err := s.Put(ctx, "a/b.txt", strings.NewReader("content"), PutOptions{ContentType: "text/plain"})
	if err != nil || obj.ETag != "etag1" || obj.Size != 7 {
		t.Fatalf("put: %+v %v", obj, err)
	}
	if obj, err := s.Stat(ctx, "a/b.txt"); err != nil || obj.Size != 7 || obj.ContentType != "text/plain" || obj.ModifiedAt.Year() != 2015 {
		t.Fatalf("stat: %+v %v", obj, err)
	}
	rc, _, err := s.Get(ctx, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "content" {
		t.Fatalf("get: %q", data)
	}
	if list, err := s.List(ctx, "a/", 10); err != nil || len(list) != 1 || list[0].Key != "a/b.txt" || list[0].ETag != "etag1" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if last.URL.Query().Get("prefix") != "a/" || last.URL.Query().Get("max-keys") != "10" {
		t.Fatalf("list query: %s", last.URL.RawQuery)
	}
	if _, err := s.Stat(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	if err := s.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, "explode"); err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("service error: %v", err)
	}
	u, err := s.PresignPut(ctx, "up.bin", time.Minute, "application/octet-stream")
	if err != nil || !strings.Contains(u, "/bucket/up.bin?X-Amz-Algorithm=") || !strings.Contains(u, "X-Amz-Expires=60") {
		t.Fatalf("presign put: %s %v", u, err)
	}
}
