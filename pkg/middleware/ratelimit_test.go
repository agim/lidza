package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimit(t *testing.T) {
	h := RateLimit(RateLimitOptions{RPS: 10, Burst: 2})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	code := func(addr string) int {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code("1.1.1.1:1") != 200 || code("1.1.1.1:2") != 200 {
		t.Fatal("burst not allowed")
	}
	if code("1.1.1.1:3") != 429 {
		t.Fatal("third request should be limited")
	}
	if code("2.2.2.2:1") != 200 {
		t.Fatal("other client limited")
	}
}

func TestLimiterRefillAndBound(t *testing.T) {
	l := &limiter{opt: RateLimitOptions{RPS: 2, Burst: 2, MaxKeys: 4}, buckets: map[string]*bucket{}}
	now := time.Now()
	l.take("a", now)
	l.take("a", now)
	if wait := l.take("a", now); wait <= 0 || wait > 500*time.Millisecond {
		t.Fatalf("wait %v", wait)
	}
	if wait := l.take("a", now.Add(time.Second)); wait != 0 {
		t.Fatalf("no refill: %v", wait)
	}
	for _, k := range []string{"b", "c", "d", "e", "f"} {
		l.take(k, now.Add(2*time.Second))
	}
	if len(l.buckets) > 4 {
		t.Fatalf("table not bounded: %d", len(l.buckets))
	}
}
