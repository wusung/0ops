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
	registry                       *prometheus.Registry
	httpTotal                      *prometheus.CounterVec
	httpDuration                   *prometheus.HistogramVec
	httpInflight                   prometheus.Gauge
	createAppPreviews              *prometheus.CounterVec
	createAppConfirms              *prometheus.CounterVec
	deployRunStateTransitions      *prometheus.CounterVec
	deployRunLeadTime              *prometheus.HistogramVec
	previewCreated                 *prometheus.CounterVec
	previewConsumed                *prometheus.CounterVec
	previewConsumeDuration         *prometheus.HistogramVec
	cloudflareTunnelConnectorsReady *prometheus.GaugeVec
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
		deployRunStateTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "zeroops",
			Name:      "deploy_run_state_transitions_total",
			Help:      "Number of deploy run state transitions by from_state, to_state, and team bucket.",
		}, []string{"state_from", "state_to", "team_bucket"}),
		deployRunLeadTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "zeroops",
			Name:      "deploy_run_lead_time_seconds",
			Help:      "Deploy run lead time (time from queued to deployed) by outcome and team bucket.",
			Buckets:   prometheus.ExponentialBuckets(10, 2, 9), // 10s to 5120s
		}, []string{"outcome", "team_bucket"}),
		previewCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "zeroops",
			Name:      "preview_created_total",
			Help:      "Number of previews created by team bucket.",
		}, []string{"team_bucket"}),
		previewConsumed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "zeroops",
			Name:      "preview_consumed_total",
			Help:      "Number of previews consumed by outcome and team bucket.",
		}, []string{"outcome", "team_bucket"}),
		previewConsumeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "zeroops",
			Name:      "preview_consume_duration_seconds",
			Help:      "Preview consume duration (time from creation to consumption) by team bucket.",
			Buckets:   prometheus.ExponentialBuckets(1, 2, 10), // 1s to 512s
		}, []string{"team_bucket"}),
		cloudflareTunnelConnectorsReady: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "zeroops",
			Name:      "cloudflare_tunnel_connectors_ready",
			Help:      "Number of ready Cloudflare tunnel connectors by region.",
		}, []string{"region"}),
	}
	reg.MustRegister(
		m.httpTotal,
		m.httpDuration,
		m.httpInflight,
		m.createAppPreviews,
		m.createAppConfirms,
		m.deployRunStateTransitions,
		m.deployRunLeadTime,
		m.previewCreated,
		m.previewConsumed,
		m.previewConsumeDuration,
		m.cloudflareTunnelConnectorsReady,
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

// ObserveDeployRunTransition records a deploy run state transition.
func (m *Metrics) ObserveDeployRunTransition(stateFrom, stateTo, teamBucket string) {
	if teamBucket == "" {
		teamBucket = "00"
	}
	m.deployRunStateTransitions.WithLabelValues(stateFrom, stateTo, teamBucket).Inc()
}

// ObserveDeployRunLeadTime records the lead time for a deploy run.
func (m *Metrics) ObserveDeployRunLeadTime(outcome, teamBucket string, duration time.Duration) {
	if outcome == "" {
		outcome = "error"
	}
	if teamBucket == "" {
		teamBucket = "00"
	}
	m.deployRunLeadTime.WithLabelValues(outcome, teamBucket).Observe(duration.Seconds())
}

// ObservePreviewCreated records a preview creation event.
func (m *Metrics) ObservePreviewCreated(teamBucket string) {
	if teamBucket == "" {
		teamBucket = "00"
	}
	m.previewCreated.WithLabelValues(teamBucket).Inc()
}

// ObservePreviewConsumed records a preview consumption event.
func (m *Metrics) ObservePreviewConsumed(outcome, teamBucket string) {
	if outcome == "" {
		outcome = "error"
	}
	if teamBucket == "" {
		teamBucket = "00"
	}
	m.previewConsumed.WithLabelValues(outcome, teamBucket).Inc()
}

// ObservePreviewConsumeDuration records the time from preview creation to consumption.
func (m *Metrics) ObservePreviewConsumeDuration(teamBucket string, duration time.Duration) {
	if teamBucket == "" {
		teamBucket = "00"
	}
	m.previewConsumeDuration.WithLabelValues(teamBucket).Observe(duration.Seconds())
}

// SetCloudflareConnectorsReady sets the number of ready Cloudflare tunnel connectors.
func (m *Metrics) SetCloudflareConnectorsReady(region string, count float64) {
	m.cloudflareTunnelConnectorsReady.WithLabelValues(region).Set(count)
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
