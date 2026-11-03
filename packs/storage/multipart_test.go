package storage

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestS3MultipartPublication(t *testing.T) {
	for _, scenario := range []string{"success", "part failed", "completion failed", "canceled", "short reader", "long reader"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var parts, calls []string
			object := "old"
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Header.Get("Authorization") == "" || r.URL.Path != "/bucket/private/object" {
					t.Error("unexpected signing or object scope")
				}
				query := r.URL.Query()
				switch {
				case r.Method == http.MethodPost && query.Has("uploads"):
					calls = append(calls, "init")
					if r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Cache-Control") != "private" || r.Header.Get("X-Amz-Acl") != "" {
						t.Error("object metadata or access changed")
					}
					fmt.Fprint(w, "<InitiateMultipartUploadResult><UploadId>token+/=</UploadId></InitiateMultipartUploadResult>")
				case r.Method == http.MethodPut && query.Get("uploadId") == "token+/=":
					number := query.Get("partNumber")
					calls = append(calls, number)
					if number == "2" && scenario == "part failed" {
						http.Error(w, "part refused", http.StatusServiceUnavailable)
						return
					}
					if number == "2" && scenario == "canceled" {
						cancel()
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					parts = append(parts, string(body))
					w.Header().Set("ETag", `"part-`+number+`"`)
				case r.Method == http.MethodPost && query.Get("uploadId") == "token+/=":
					calls = append(calls, "complete")
					var req struct {
						Parts []multipartPart `xml:"Part"`
					}
					if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					want := []multipartPart{{1, `"part-1"`}, {2, `"part-2"`}, {3, `"part-3"`}}
					if !slices.Equal(req.Parts, want) {
						t.Errorf("completion order or ETags changed: %v", req.Parts)
					}
					if scenario == "completion failed" {
						fmt.Fprint(w, " \n<Error><Code>InternalError</Code></Error>")
						return
					}
					object = strings.Join(parts, "")
					fmt.Fprint(w, "<CompleteMultipartUploadResult><ETag>\"complete-3\"</ETag></CompleteMultipartUploadResult>")
				case r.Method == http.MethodDelete && query.Get("uploadId") == "token+/=":
					calls = append(calls, "abort")
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected multipart request %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			})
			server := httptest.NewServer(handler)
			defer server.Close()
			provider, err := newS3(Config{Endpoint: server.URL, Bucket: "bucket", Region: "region", AccessKey: "test-key", SecretKey: "test-secret"})
			if err != nil {
				t.Fatal(err)
			}
			input := "abcdefghijkl"
			switch scenario {
			case "short reader":
				input = input[:6]
			case "long reader":
				input += "m"
			}
			got, err := provider.putMultipart(ctx, "private/object", strings.NewReader(input), 12, 5, PutOptions{ContentType: "application/octet-stream", CacheControl: "private"})
			mu.Lock()
			defer mu.Unlock()
			if scenario == "success" {
				if err != nil || got.ETag != "complete-3" || got.Size != 12 || object != input || !slices.Equal(calls, []string{"init", "1", "2", "3", "complete"}) {
					t.Fatalf("unexpected publication: object=%q calls=%v meta=%+v error=%v", object, calls, got, err)
				}
			} else if err == nil || object != "old" || len(calls) == 0 || calls[len(calls)-1] != "abort" {
				t.Fatalf("expected incomplete upload to abort: object=%q calls=%v error=%v", object, calls, err)
			}
		})
	}
}
