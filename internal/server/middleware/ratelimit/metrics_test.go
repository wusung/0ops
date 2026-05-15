package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestRegisterMetricsAndIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	obs := MustRegisterMetrics(reg)
	obs.RecordTriggered(ScopePerToken, CategoryWrite, PlanFree)
	obs.RecordTriggered(ScopePerTeam, CategoryPreviewCreate, PlanPro)

	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()

	if !strings.Contains(body, `zeroops_rate_limit_triggered_total{category="write",plan="free",scope="per_token"} 1`) {
		t.Fatalf("missing per-token write counter: %s", body)
	}
	if !strings.Contains(body, `zeroops_rate_limit_triggered_total{category="preview_create",plan="pro",scope="per_team"} 1`) {
		t.Fatalf("missing per-team preview counter: %s", body)
	}
}

func TestNoopObserverDoesNotPanic(t *testing.T) {
	NoopObserver{}.RecordTriggered(ScopePerToken, CategoryWrite, PlanFree)
}
