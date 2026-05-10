package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry        *prometheus.Registry
	httpDuration    *prometheus.HistogramVec
	httpInflight    prometheus.Gauge
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	m := &Metrics{
		registry: reg,
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "zeroops",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency by route, method, and status.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"route", "method", "status"}),
		httpInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "zeroops",
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "Current number of HTTP requests being served.",
		}),
	}
	reg.MustRegister(m.httpDuration, m.httpInflight)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Middleware records request duration and in-flight counts. The route label
// is provided by chi via chi.RouteContext after routing; callers may pass
// a static fallback label if the route is unknown.
func (m *Metrics) Middleware(routeLabel func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			m.httpInflight.Inc()
			defer m.httpInflight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			route := routeLabel(r)
			m.httpDuration.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).
				Observe(time.Since(start).Seconds())
		})
	}
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
