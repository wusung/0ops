// Package observability provides HTTP metrics helpers.
package observability

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus registry and HTTP collectors.
type Metrics struct {
	registry          *prometheus.Registry
	httpTotal         *prometheus.CounterVec
	httpDuration      *prometheus.HistogramVec
	httpInflight      prometheus.Gauge
	createAppPreviews *prometheus.CounterVec
	createAppConfirms *prometheus.CounterVec
}

// NewMetrics creates the default HTTP metrics registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	m := &Metrics{
		registry: reg,
		httpTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "zeroops",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "HTTP requests by route, method, status, and team bucket.",
		}, []string{"route", "method", "status", "team_bucket"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "zeroops",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency by route, method, and team bucket.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"route", "method", "team_bucket"}),
		httpInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "zeroops",
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "Current number of HTTP requests being served.",
		}),
		createAppPreviews: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "zeroops",
			Name:      "create_app_previews_total",
			Help:      "Number of create_app preview requests by outcome.",
		}, []string{"outcome"}),
		createAppConfirms: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "zeroops",
			Name:      "create_app_confirms_total",
			Help:      "Number of create_app confirm requests by outcome and replay flag.",
		}, []string{"outcome", "idempotent_replay"}),
	}
	reg.MustRegister(
		m.httpTotal,
		m.httpDuration,
		m.httpInflight,
		m.createAppPreviews,
		m.createAppConfirms,
	)
	return m
}

// Handler returns the Prometheus scrape handler.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Middleware records request duration and in-flight counts. The route label
// is provided by chi via chi.RouteContext after routing; callers may pass
// a static fallback label if the route is unknown.
// Middleware records duration and in-flight counts for HTTP requests.
func (m *Metrics) Middleware(routeLabel func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			m.httpInflight.Inc()
			defer m.httpInflight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			route := routeLabel(r)
			teamBucket := teamBucketForRequest(r)
			m.httpTotal.WithLabelValues(route, r.Method, strconv.Itoa(rec.status), teamBucket).Inc()
			m.httpDuration.WithLabelValues(route, r.Method, teamBucket).
				Observe(time.Since(start).Seconds())
		})
	}
}

// ObserveCreateAppPreview records create_app preview outcome.
func (m *Metrics) ObserveCreateAppPreview(outcome string) {
	if outcome == "" {
		outcome = "error"
	}
	m.createAppPreviews.WithLabelValues(outcome).Inc()
}

// ObserveCreateAppConfirm records create_app confirm outcome.
func (m *Metrics) ObserveCreateAppConfirm(outcome string, idempotentReplay bool) {
	if outcome == "" {
		outcome = "error"
	}
	replay := "false"
	if idempotentReplay {
		replay = "true"
	}
	m.createAppConfirms.WithLabelValues(outcome, replay).Inc()
}

func teamBucketForRequest(r *http.Request) string {
	const prefix = "/v1/teams/"
	path := r.URL.Path
	if !strings.HasPrefix(path, prefix) {
		return "global"
	}
	rest := strings.TrimPrefix(path, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "team"
	}
	team := rest[:slash]
	if team == "" {
		return "team"
	}
	if len(team) == 1 {
		return strings.ToLower(team)
	}
	return strings.ToLower(team[:2])
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}
