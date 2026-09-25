package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestS3Live runs against an S3-compatible server on STORAGE_TEST_ENDPOINT
// (default http://127.0.0.1:9000: MinIO or RustFS with the keys lidza /
// lidzalidza, or STORAGE_TEST_ACCESS_KEY and STORAGE_TEST_SECRET_KEY);
// skipped when nothing answers there. It is the proof of the S3 wire
// format and the signatures against a real implementation.
func TestS3Live(t *testing.T) {
	endpoint := or(os.Getenv("STORAGE_TEST_ENDPOINT"), "http://127.0.0.1:9000")
	if res, err := (&http.Client{Timeout: 2 * time.Second}).Get(endpoint + "/"); err != nil {
		t.Skipf("no S3 server at %s: %v", endpoint, err)
	} else {
		res.Body.Close()
	}
	s, err := New(Config{Provider: "s3", Bucket: "lidza-test", Endpoint: endpoint, AccessKey: or(os.Getenv("STORAGE_TEST_ACCESS_KEY"), "lidza"), SecretKey: or(os.Getenv("STORAGE_TEST_SECRET_KEY"), "lidzalidza")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatalf("bucket exists: %v", err)
	}
	key := "live/" + time.Now().UTC().Format("20060102T150405") + "/hello world.txt"
	obj, err := s.Put(ctx, key, strings.NewReader("hello, minio"), PutOptions{CacheControl: "no-store"})
	if err != nil || obj.ContentType != "text/plain; charset=utf-8" || obj.ETag == "" {
		t.Fatalf("put: %+v %v", obj, err)
	}
	stat, err := s.Stat(ctx, key)
	if err != nil || stat.Size != 12 || stat.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("stat: %+v %v", stat, err)
	}
	rc, _, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello, minio" {
		t.Fatalf("get: %q", data)
	}
	list, err := s.List(ctx, "live/", 1000)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	found := false
	for _, o := range list {
		found = found || o.Key == key
	}
	if !found {
		t.Fatalf("listed keys lack %s: %+v", key, list)
	}
	// Presigned read and write, used by a plain HTTP client as a browser
	// would.
	u, err := s.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(got) != "hello, minio" {
		t.Fatalf("presigned get: %d %q", res.StatusCode, got)
	}
	up, err := s.PresignPut(ctx, key+".up", time.Minute, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPut, up, bytes.NewReader([]byte{1, 2, 3}))
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("presigned put: %d", res.StatusCode)
	}
	if st, err := s.Stat(ctx, key+".up"); err != nil || st.Size != 3 {
		t.Fatalf("uploaded through the presigned URL: %+v %v", st, err)
	}
	for _, k := range []string{key, key + ".up"} {
		if err := s.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Stat(ctx, key); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
