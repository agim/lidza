package middleware

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimitOptions configures RateLimit.
type RateLimitOptions struct {
	// RPS is the sustained rate per key; Burst the bucket size.
	RPS   float64
	Burst int
	// Key groups requests; default is the client IP from RemoteAddr.
	// Behind a trusted proxy, key by the forwarded address instead.
	Key func(r *http.Request) string
	// MaxKeys bounds the table of buckets; when reached, buckets idle for
	// the longest are evicted. Default 100000.
	MaxKeys int
}

// RateLimit is a token bucket per key. Over the limit replies 429 with
// Retry-After. The bucket table is bounded, so a flood of new clients
// cannot grow memory without limit.
func RateLimit(o RateLimitOptions) Middleware {
	if o.RPS <= 0 {
		o.RPS = 10
	}
	if o.Burst <= 0 {
		o.Burst = int(math.Max(1, o.RPS))
	}
	if o.MaxKeys <= 0 {
		o.MaxKeys = 100000
	}
	if o.Key == nil {
		o.Key = ClientIP
	}
	l := &limiter{opt: o, buckets: map[string]*bucket{}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wait := l.take(o.Key(r), time.Now()); wait > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"rate limit exceeded"}` + "\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP is the default rate-limit key: the connection's remote host.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	opt     RateLimitOptions
	mu      sync.Mutex
	buckets map[string]*bucket
}

// take consumes one token for key, returning how long the caller must
// wait when none is available.
func (l *limiter) take(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.opt.MaxKeys {
			l.evict(now)
		}
		b = &bucket{tokens: float64(l.opt.Burst), last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(float64(l.opt.Burst), b.tokens+now.Sub(b.last).Seconds()*l.opt.RPS)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) / l.opt.RPS * float64(time.Second))
}

// evict drops the idlest half of the table.
func (l *limiter) evict(now time.Time) {
	cutoff := time.Minute
	for len(l.buckets) >= l.opt.MaxKeys/2 && cutoff > 0 {
		for k, b := range l.buckets {
			if now.Sub(b.last) >= cutoff {
				delete(l.buckets, k)
			}
		}
		cutoff /= 2
	}
	for k := range l.buckets {
		if len(l.buckets) < l.opt.MaxKeys/2 {
			break
		}
		delete(l.buckets, k)
	}
}
