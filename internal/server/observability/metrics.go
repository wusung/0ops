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
	previewCreated    *prometheus.CounterVec
	previewConsumed   *prometheus.CounterVec
	previewConsumeDur *prometheus.HistogramVec
	deployRunTerminal *prometheus.CounterVec
	deployRunLeadTime prometheus.Histogram
	deployRunFailures *prometheus.CounterVec
	cloudflareAPICall *prometheus.CounterVec
	cloudflareAPIDur  *prometheus.HistogramVec
	tunnelConnectors  prometheus.Gauge
	domainVerify      *prometheus.CounterVec
	reconPending      *prometheus.GaugeVec
	createAppPreviews   *prometheus.CounterVec
	createAppConfirms   *prometheus.CounterVec
	rateLimitTriggered  *prometheus.CounterVec
	reconTick           *prometheus.CounterVec
	reconJobTerminal    *prometheus.CounterVec
	reconClassification *prometheus.CounterVec
	incidentsOpened     *prometheus.CounterVec
	incidentsClosed     *prometheus.CounterVec
	incidentsOpen       *prometheus.GaugeVec
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
		previewCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_preview_created_total",
			Help: "Number of previews created by action type.",
		}, []string{"action"}),
		previewConsumed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_preview_consumed_total",
			Help: "Number of consumed previews by action and outcome.",
		}, []string{"action", "outcome"}),
		previewConsumeDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "zeroops_preview_consume_duration_seconds",
			Help:    "Duration between preview creation and consume.",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 10),
		}, []string{"action", "outcome"}),
		deployRunTerminal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_deploy_run_terminal_total",
			Help: "Number of deploy runs reaching terminal outcomes.",
		}, []string{"outcome"}),
		deployRunLeadTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "zeroops_deploy_run_lead_time_seconds",
			Help:    "Lead time from dispatch to terminal deploy status.",
			Buckets: prometheus.ExponentialBuckets(5, 2, 12),
		}),
		deployRunFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_deploy_run_failures_total",
			Help: "Deploy failures by stage and classification.",
		}, []string{"stage", "classification"}),
		cloudflareAPICall: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_cloudflare_api_calls_total",
			Help: "Cloudflare API calls by operation and outcome.",
		}, []string{"op", "outcome"}),
		cloudflareAPIDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "zeroops_cloudflare_api_call_duration_seconds",
			Help:    "Cloudflare API call latency by operation.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
		}, []string{"op"}),
		tunnelConnectors: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "zeroops_cloudflare_tunnel_connectors_ready",
			Help: "Number of cloudflared tunnel connectors currently ready (0..N).",
		}),
		domainVerify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_domain_verify_attempts_total",
			Help: "Domain verify attempts by outcome.",
		}, []string{"outcome"}),
		reconPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "zeroops_reconciliation_jobs_pending",
			Help: "Pending reconciliation jobs grouped by kind.",
		}, []string{"kind"}),
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
		rateLimitTriggered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_rate_limit_triggered_total",
			Help: "Number of rate-limited (HTTP 429) responses, labelled by scope, category, and plan tier.",
		}, []string{"scope", "category", "plan"}),
		reconTick: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_reconciler_tick_total",
			Help: "Reconciler loop ticks, labelled by loop kind and outcome (success / error / skipped_not_leader).",
		}, []string{"kind", "outcome"}),
		reconJobTerminal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_reconciler_job_terminal_total",
			Help: "Reconciliation jobs reaching a terminal status (completed / failed_permanently), labelled by job kind.",
		}, []string{"kind", "outcome"}),
		reconClassification: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_deploy_run_failure_classification_total",
			Help: "Deploy failures attributed by reconciler classification (spec § 7.1 enum). Used for the unknown-share dashboard panel.",
		}, []string{"classification"}),
		incidentsOpened: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_incident_opened_total",
			Help: "Incidents opened, labelled by kind and severity (reconciler-and-incident spec § 9.2).",
		}, []string{"kind", "severity"}),
		incidentsClosed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeroops_incident_closed_total",
			Help: "Incidents closed via CLI, labelled by kind and severity.",
		}, []string{"kind", "severity"}),
		incidentsOpen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "zeroops_incident_open",
			Help: "Currently open incidents, grouped by severity.",
		}, []string{"severity"}),
	}
	reg.MustRegister(
		m.httpTotal,
		m.httpDuration,
		m.httpInflight,
		m.previewCreated,
		m.previewConsumed,
		m.previewConsumeDur,
		m.deployRunTerminal,
		m.deployRunLeadTime,
		m.deployRunFailures,
		m.cloudflareAPICall,
		m.cloudflareAPIDur,
		m.tunnelConnectors,
		m.domainVerify,
		m.reconPending,
		m.createAppPreviews,
		m.createAppConfirms,
		m.rateLimitTriggered,
		m.reconTick,
		m.reconJobTerminal,
		m.reconClassification,
		m.incidentsOpened,
		m.incidentsClosed,
		m.incidentsOpen,
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
	if outcome == "success" {
		m.ObservePreviewCreated("create_app")
	}
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

// ObservePreviewCreated records preview creation by action.
func (m *Metrics) ObservePreviewCreated(action string) {
	if action == "" {
		action = "unknown"
	}
	m.previewCreated.WithLabelValues(action).Inc()
}

// ObservePreviewConsumed records preview consumption and consume latency.
func (m *Metrics) ObservePreviewConsumed(action, outcome string, latency time.Duration) {
	if action == "" {
		action = "unknown"
	}
	if outcome == "" {
		outcome = "failed"
	}
	if latency < 0 {
		latency = 0
	}
	m.previewConsumed.WithLabelValues(action, outcome).Inc()
	m.previewConsumeDur.WithLabelValues(action, outcome).Observe(latency.Seconds())
}

// ObserveDeployRunTerminal records a terminal deploy outcome.
func (m *Metrics) ObserveDeployRunTerminal(outcome string) {
	if outcome == "" {
		outcome = "unknown"
	}
	m.deployRunTerminal.WithLabelValues(outcome).Inc()
}

// ObserveDeployRunLeadTime records deploy lead time in seconds.
func (m *Metrics) ObserveDeployRunLeadTime(latency time.Duration) {
	if latency < 0 {
		latency = 0
	}
	m.deployRunLeadTime.Observe(latency.Seconds())
}

// ObserveDeployRunFailure records deploy failures by stage and classification.
func (m *Metrics) ObserveDeployRunFailure(stage, classification string) {
	if stage == "" {
		stage = "unknown"
	}
	if classification == "" {
		classification = "unknown"
	}
	m.deployRunFailures.WithLabelValues(stage, classification).Inc()
}

// ObserveCloudflareAPICall records a Cloudflare API operation outcome.
func (m *Metrics) ObserveCloudflareAPICall(op, outcome string) {
	if op == "" {
		op = "unknown"
	}
	if outcome == "" {
		outcome = "error"
	}
	m.cloudflareAPICall.WithLabelValues(op, outcome).Inc()
}

// ObserveCloudflareAPICallDuration records Cloudflare API call latency by op.
func (m *Metrics) ObserveCloudflareAPICallDuration(op string, latency time.Duration) {
	if op == "" {
		op = "unknown"
	}
	if latency < 0 {
		latency = 0
	}
	m.cloudflareAPIDur.WithLabelValues(op).Observe(latency.Seconds())
}

// SetCloudflareTunnelConnectorsReady sets current number of cloudflared
// connectors reported ready (0..N). Populated by the tunnel reconciler.
func (m *Metrics) SetCloudflareTunnelConnectorsReady(count float64) {
	if count < 0 {
		count = 0
	}
	m.tunnelConnectors.Set(count)
}

// ObserveDomainVerifyAttempt records domain verification attempts by outcome.
func (m *Metrics) ObserveDomainVerifyAttempt(outcome string) {
	if outcome == "" {
		outcome = "failed"
	}
	m.domainVerify.WithLabelValues(outcome).Inc()
}

// ObserveRateLimitTriggered increments the rate-limit trigger counter for one
// 429 emission. Labels are unconstrained strings so the caller controls the
// allowed values (callers must use a fixed enumeration; see
// internal/server/middleware/ratelimit.Plan / Scope / Category for the
// canonical 4 × 2 × 3 set, hard-rule §14 #8).
func (m *Metrics) ObserveRateLimitTriggered(scope, category, plan string) {
	m.rateLimitTriggered.WithLabelValues(scope, category, plan).Inc()
}

// RateLimitObserver returns a function-shape observer hook over the
// rate-limit counter; downstream packages can wrap it without depending on
// observability internals.
func (m *Metrics) RateLimitObserver() func(scope, category, plan string) {
	return m.ObserveRateLimitTriggered
}

// SetReconciliationJobsPending sets pending reconciliation jobs count by kind.
func (m *Metrics) SetReconciliationJobsPending(kind string, count float64) {
	if kind == "" {
		kind = "unknown"
	}
	if count < 0 {
		count = 0
	}
	m.reconPending.WithLabelValues(kind).Set(count)
}

// ObserveReconcilerTick records one reconciler loop tick result.
func (m *Metrics) ObserveReconcilerTick(kind, outcome string) {
	if kind == "" {
		kind = "unknown"
	}
	if outcome == "" {
		outcome = "unknown"
	}
	m.reconTick.WithLabelValues(kind, outcome).Inc()
}

// ObserveReconcilerJobTerminal records one terminal reconciliation_job
// outcome (completed / failed_permanently).
func (m *Metrics) ObserveReconcilerJobTerminal(kind, outcome string) {
	if kind == "" {
		kind = "unknown"
	}
	if outcome == "" {
		outcome = "unknown"
	}
	m.reconJobTerminal.WithLabelValues(kind, outcome).Inc()
}

// ObserveFailureClassification records every failed deploy_run by its
// classification value (spec § 7.3 panel input).
func (m *Metrics) ObserveFailureClassification(classification string) {
	if classification == "" {
		classification = "unknown"
	}
	m.reconClassification.WithLabelValues(classification).Inc()
}

// ObserveIncidentOpened increments the incident-opened counter.
func (m *Metrics) ObserveIncidentOpened(kind, severity string) {
	if kind == "" {
		kind = "unknown"
	}
	if severity == "" {
		severity = "medium"
	}
	m.incidentsOpened.WithLabelValues(kind, severity).Inc()
}

// ObserveIncidentClosed increments the incident-closed counter.
func (m *Metrics) ObserveIncidentClosed(kind, severity string) {
	if kind == "" {
		kind = "unknown"
	}
	if severity == "" {
		severity = "medium"
	}
	m.incidentsClosed.WithLabelValues(kind, severity).Inc()
}

// SetOpenIncidents sets the current number of open incidents grouped
// by severity (drives the dashboard "open incidents" panel).
func (m *Metrics) SetOpenIncidents(severity string, count float64) {
	if severity == "" {
		severity = "medium"
	}
	if count < 0 {
		count = 0
	}
	m.incidentsOpen.WithLabelValues(severity).Set(count)
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
