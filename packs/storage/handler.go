package storage

import (
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Handler serves objects by key, for the local provider in development
// and for private buckets the app fronts: mount it behind the access
// check the objects need, e.g.
//
//	r.Group("/api/v1/files", auth.Require()).Handle("GET /api/v1/files/{key...}", storage.Handler("/api/v1/files/"))
//
// prefix is the mounted path, stripped to get the key.
func Handler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, prefix)
		rc, obj, err := From(r.Context()).Get(r.Context(), key)
		if err == ErrNotFound {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		defer rc.Close()
		if obj.ContentType != "" {
			w.Header().Set("Content-Type", obj.ContentType)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			return
		}
		io.Copy(w, rc)
	})
}
