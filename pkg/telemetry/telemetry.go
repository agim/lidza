// Package telemetry gives every app /metrics (Prometheus exposition,
// OpenTelemetry-compatible), /healthz (liveness) and /readyz (readiness:
// every service that can check itself is asked). Request metrics are
// labelled by route pattern, never by raw path, so cardinality stays
// bounded.
package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Ready is implemented by services that can report readiness: a database
// pool that pings, a bus that is connected. /readyz fails while any
// returns an error.
type Ready interface {
	Ready(ctx context.Context) error
}

// Stats is implemented by services that expose gauges: pool sizes, open
// connections, calls in flight. Keys are metric-safe names.
type Stats interface {
	TelemetryStats() map[string]float64
}

// Named services label their stats; otherwise the Go type name is used.
type Named interface {
	Name() string
}

// Telemetry is the registry and the collectors of one app.
type Telemetry struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inflight prometheus.Gauge
	each     func(func(any))
}

// New builds a registry with the Go runtime and process collectors, the
// request metrics and a collector that walks services (each is called
// with every registered service).
func New(each func(func(any))) *Telemetry {
	t := &Telemetry{
		reg:  prometheus.NewRegistry(),
		each: each,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lidza_http_requests_total", Help: "Requests by method, route pattern and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "lidza_http_request_duration_seconds", Help: "Request duration by method and route pattern.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{Name: "lidza_http_requests_in_flight", Help: "Requests being served."}),
	}
	t.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		t.requests, t.duration, t.inflight, &servicesCollector{t: t})
	return t
}

// Registry exposes the registry for packs that add their own metrics.
func (t *Telemetry) Registry() *prometheus.Registry { return t.reg }

// Middleware records every request. Place it directly around the router
// so the matched pattern (http.Request.Pattern) is visible.
func (t *Telemetry) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			t.inflight.Inc()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			t.inflight.Dec()
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			t.requests.WithLabelValues(r.Method, route, strconv.Itoa(sw.status())).Inc()
			t.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		})
	}
}

// Metrics serves the Prometheus exposition.
func (t *Telemetry) Metrics() http.Handler {
	return promhttp.HandlerFor(t.reg, promhttp.HandlerOpts{})
}

// Healthz is liveness: the process serves requests.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
}

// Readyz asks every Ready service, with a deadline, and replies 503 while
// any fails. The body lists each check.
func (t *Telemetry) Readyz(timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		checks := map[string]string{}
		ok := true
		var mu sync.Mutex
		var wg sync.WaitGroup
		t.each(func(svc any) {
			rd, isReady := svc.(Ready)
			if !isReady {
				return
			}
			name := serviceName(svc)
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := rd.Ready(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					checks[name] = err.Error()
					ok = false
				} else {
					checks[name] = "ok"
				}
			}()
		})
		wg.Wait()
		w.Header().Set("Content-Type", "application/json")
		status := "ok"
		if !ok {
			status = "not ready"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(map[string]any{"status": status, "checks": checks})
	})
}

// servicesCollector exports every Stats service as gauges
// lidza_service_stat{service, stat}.
type servicesCollector struct {
	t    *Telemetry
	desc *prometheus.Desc
}

func (c *servicesCollector) Describe(ch chan<- *prometheus.Desc) {
	if c.desc == nil {
		c.desc = prometheus.NewDesc("lidza_service_stat", "Gauges reported by services (pools, connections).", []string{"service", "stat"}, nil)
	}
	ch <- c.desc
}

func (c *servicesCollector) Collect(ch chan<- prometheus.Metric) {
	if c.desc == nil {
		c.Describe(make(chan *prometheus.Desc, 1))
	}
	c.t.each(func(svc any) {
		st, ok := svc.(Stats)
		if !ok {
			return
		}
		name := serviceName(svc)
		stats := st.TelemetryStats()
		keys := make([]string, 0, len(stats))
		for k := range stats {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, stats[k], name, k)
		}
	})
}

func serviceName(svc any) string {
	if n, ok := svc.(Named); ok {
		return n.Name()
	}
	return typeName(svc)
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.code == 0 {
		s.code = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) status() int {
	if s.code == 0 {
		return http.StatusOK
	}
	return s.code
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
