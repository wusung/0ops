package ratelimit

import "github.com/prometheus/client_golang/prometheus"

// Observer records rate-limit telemetry.
type Observer interface {
	RecordTriggered(scope Scope, cat Category, plan Plan)
}

// MetricsObserver implements Observer backed by a Prometheus counter.
type MetricsObserver struct {
	triggered *prometheus.CounterVec
}

// NoopObserver discards observations; used in tests when metrics aren't
// part of the assertion surface.
type NoopObserver struct{}

// RecordTriggered satisfies Observer; intentionally no-op.
func (NoopObserver) RecordTriggered(_ Scope, _ Category, _ Plan) {}

// MustRegisterMetrics registers the rate-limit metrics on reg and returns
// an Observer. Panics on duplicate registration.
func MustRegisterMetrics(reg prometheus.Registerer) *MetricsObserver {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "zeroops_rate_limit_triggered_total",
		Help: "Number of rate-limited (HTTP 429) responses, labelled by scope, category, and plan tier.",
	}, []string{"scope", "category", "plan"})
	reg.MustRegister(c)
	return &MetricsObserver{triggered: c}
}

// RecordTriggered increments the triggered counter for one 429 emission.
func (m *MetricsObserver) RecordTriggered(scope Scope, cat Category, plan Plan) {
	if m == nil || m.triggered == nil {
		return
	}
	m.triggered.WithLabelValues(string(scope), string(cat), string(plan)).Inc()
}

// ObserverFunc adapts a plain function into an Observer; used to bridge
// observability/metrics.go (which exposes string-shaped helpers) into
// this package's typed interface.
type ObserverFunc func(scope Scope, cat Category, plan Plan)

// RecordTriggered satisfies Observer.
func (f ObserverFunc) RecordTriggered(scope Scope, cat Category, plan Plan) {
	if f == nil {
		return
	}
	f(scope, cat, plan)
}
