package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsMiddlewareAndHandlerExposeCustomSeries(t *testing.T) {
	metrics := NewMetrics()
	handler := metrics.Middleware(func(*http.Request) string { return "/health" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_http_request_duration_seconds_count{method="GET",route="/health",status="204"} 1`) {
		t.Fatalf("metrics output missing request duration count: %s", body)
	}
	if !strings.Contains(body, "# HELP zeroops_http_requests_in_flight Current number of HTTP requests being served.") {
		t.Fatalf("metrics output missing inflight gauge help: %s", body)
	}
}
