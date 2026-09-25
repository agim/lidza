package lidza

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
)

// Pack is a unit the app starts and stops: an official or local pack. Start
// registers whatever it offers (a pool, a client) with Provide.
type Pack interface {
	Name() string
	Start(ctx context.Context, s *Services) error
	Stop(ctx context.Context) error
}

// Services holds what packs and OnStart provide, keyed by static type, and
// hands it to handlers through the request context. It is filled at start
// and read-only afterwards.
type Services struct {
	mu     sync.RWMutex
	values map[reflect.Type]any
}

// NewServices returns an empty registry.
func NewServices() *Services { return &Services{values: map[reflect.Type]any{}} }

// Provide registers v under its static type T. Providing the same type
// twice replaces the first value.
func Provide[T any](s *Services, v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[reflect.TypeFor[T]()] = v
}

// Lookup returns the value registered for t.
func (s *Services) Lookup(t reflect.Type) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[t]
	return v, ok
}

type servicesKey struct{}

// WithServices attaches s to ctx; the app handler does this for every
// request, tests do it by hand.
func WithServices(ctx context.Context, s *Services) context.Context {
	return context.WithValue(ctx, servicesKey{}, s)
}

// Service returns the T provided at start. A missing service is a wiring
// mistake, not a runtime condition, so it panics with the type name.
func Service[T any](ctx context.Context) T {
	s, _ := ctx.Value(servicesKey{}).(*Services)
	t := reflect.TypeFor[T]()
	if s != nil {
		if v, ok := s.Lookup(t); ok {
			return v.(T)
		}
	}
	panic(fmt.Sprintf("lidza: no service of type %s; is its pack enabled in lidza.json?", t))
}

func servicesMiddleware(s *Services) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(WithServices(r.Context(), s)))
		})
	}
}
