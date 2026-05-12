package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsMiddlewareAndHandlerExposeCustomSeries(t *testing.T) {
	metrics := NewMetrics()
	handler := metrics.Middleware(func(*http.Request) string { return "/health" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/teams/acme/apps", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_http_request_duration_seconds_count{method="GET",route="/health",team_bucket="ac"} 1`) {
		t.Fatalf("metrics output missing request duration count: %s", body)
	}
	if !strings.Contains(body, `zeroops_http_requests_total{method="GET",route="/health",status="204",team_bucket="ac"} 1`) {
		t.Fatalf("metrics output missing request total: %s", body)
	}
	if !strings.Contains(body, "# HELP zeroops_http_requests_in_flight Current number of HTTP requests being served.") {
		t.Fatalf("metrics output missing inflight gauge help: %s", body)
	}
}

func TestCreateAppMetricsExposeSeries(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveCreateAppPreview("success")
	metrics.ObserveCreateAppConfirm("success", false)
	metrics.ObserveCreateAppConfirm("success", true)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_create_app_previews_total{outcome="success"} 1`) {
		t.Fatalf("metrics output missing create_app preview counter: %s", body)
	}
	if !strings.Contains(body, `zeroops_create_app_confirms_total{idempotent_replay="false",outcome="success"} 1`) {
		t.Fatalf("metrics output missing create_app confirm counter (first): %s", body)
	}
	if !strings.Contains(body, `zeroops_create_app_confirms_total{idempotent_replay="true",outcome="success"} 1`) {
		t.Fatalf("metrics output missing create_app confirm counter (replay): %s", body)
	}
}

func TestDeployRunMetrics(t *testing.T) {
	m := NewMetrics()

	// Test state transitions
	m.ObserveDeployRunTransition("queued", "preparing", "00")
	m.ObserveDeployRunTransition("preparing", "building", "00")
	m.ObserveDeployRunLeadTime("success", "00", 5*time.Second)

	// Verify via scrape
	handler := m.Handler()
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest("GET", "/metrics", nil))

	body := resp.Body.String()
	if !strings.Contains(body, `deploy_run_state_transitions_total{state_from="queued",state_to="preparing",team_bucket="00"} 1`) {
		t.Error("deploy_run_state_transitions_total not found in metrics")
	}
	if !strings.Contains(body, `deploy_run_lead_time_seconds_bucket`) {
		t.Error("deploy_run_lead_time_seconds not found")
	}
}

func TestPreviewMetrics(t *testing.T) {
	m := NewMetrics()

	m.ObservePreviewCreated("00")
	m.ObservePreviewConsumed("success", "00")
	m.ObservePreviewConsumeDuration("00", 30*time.Second)

	handler := m.Handler()
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest("GET", "/metrics", nil))

	body := resp.Body.String()
	if !strings.Contains(body, `preview_created_total{team_bucket="00"} 1`) {
		t.Error("preview_created_total not found")
	}
	if !strings.Contains(body, `preview_consumed_total{outcome="success",team_bucket="00"} 1`) {
		t.Error("preview_consumed_total not found")
	}
}

func TestCloudflareMetrics(t *testing.T) {
	m := NewMetrics()
	m.SetCloudflareConnectorsReady("us-west", 2)

	handler := m.Handler()
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest("GET", "/metrics", nil))

	body := resp.Body.String()
	if !strings.Contains(body, `cloudflare_tunnel_connectors_ready{region="us-west"} 2`) {
		t.Error("cloudflare_tunnel_connectors_ready not found")
	}
}
