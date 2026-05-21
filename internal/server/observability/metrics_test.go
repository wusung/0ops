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

func TestM2_6ObservabilitySeriesExpose(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObservePreviewCreated("create_app")
	metrics.ObservePreviewConsumed("create_app", "success", 12*time.Second)
	metrics.ObservePreviewConsumed("create_app", "idempotent_replay", 3*time.Second)
	metrics.ObserveDeployRunTerminal("success")
	metrics.ObserveDeployRunTerminal("failed")
	metrics.ObserveDeployRunLeadTime(5 * time.Minute)
	metrics.ObserveDeployRunFailure("render", "gitops_push_conflict")
	metrics.ObserveCloudflareAPICall("dns_create", "success")
	metrics.ObserveCloudflareAPICall("hostname_create", "throttled")
	metrics.ObserveDomainVerifyAttempt("success")
	metrics.ObserveDomainVerifyAttempt("failed")
	metrics.SetReconciliationJobsPending("stuck_deploy", 2)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_preview_created_total{action="create_app"} 1`) {
		t.Fatalf("metrics output missing preview_created_total: %s", body)
	}
	if !strings.Contains(body, `zeroops_preview_consumed_total{action="create_app",outcome="success"} 1`) {
		t.Fatalf("metrics output missing preview_consumed_total success: %s", body)
	}
	if !strings.Contains(body, `zeroops_preview_consumed_total{action="create_app",outcome="idempotent_replay"} 1`) {
		t.Fatalf("metrics output missing preview_consumed_total replay: %s", body)
	}
	if !strings.Contains(body, `zeroops_preview_consume_duration_seconds_count{action="create_app",outcome="success"} 1`) {
		t.Fatalf("metrics output missing preview consume duration series: %s", body)
	}
	if !strings.Contains(body, `zeroops_deploy_run_terminal_total{outcome="success"} 1`) {
		t.Fatalf("metrics output missing deploy terminal success: %s", body)
	}
	if !strings.Contains(body, `zeroops_deploy_run_terminal_total{outcome="failed"} 1`) {
		t.Fatalf("metrics output missing deploy terminal failed: %s", body)
	}
	if !strings.Contains(body, `zeroops_deploy_run_lead_time_seconds_count 1`) {
		t.Fatalf("metrics output missing deploy lead time histogram: %s", body)
	}
	if !strings.Contains(body, `zeroops_cloudflare_api_calls_total{op="dns_create",outcome="success"} 1`) {
		t.Fatalf("metrics output missing cloudflare_api_calls_total success: %s", body)
	}
	if !strings.Contains(body, `zeroops_cloudflare_api_calls_total{op="hostname_create",outcome="throttled"} 1`) {
		t.Fatalf("metrics output missing cloudflare_api_calls_total throttled: %s", body)
	}
	if !strings.Contains(body, `zeroops_deploy_run_failures_total{classification="gitops_push_conflict",stage="render"} 1`) {
		t.Fatalf("metrics output missing deploy_run_failures_total: %s", body)
	}
	if !strings.Contains(body, `zeroops_domain_verify_attempts_total{outcome="success"} 1`) {
		t.Fatalf("metrics output missing domain_verify_attempts_total success: %s", body)
	}
	if !strings.Contains(body, `zeroops_domain_verify_attempts_total{outcome="failed"} 1`) {
		t.Fatalf("metrics output missing domain_verify_attempts_total failed: %s", body)
	}
	if !strings.Contains(body, `zeroops_reconciliation_jobs_pending{kind="stuck_deploy"} 2`) {
		t.Fatalf("metrics output missing reconciliation_jobs_pending gauge: %s", body)
	}
}

func TestM2_5CloudflareTunnelMetricsExpose(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveCloudflareAPICallDuration("dns_list", 250*time.Millisecond)
	metrics.SetCloudflareTunnelConnectorsReady(3)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_cloudflare_api_call_duration_seconds_count{op="dns_list"} 1`) {
		t.Fatalf("metrics output missing cloudflare api call duration histogram: %s", body)
	}
	if !strings.Contains(body, `zeroops_cloudflare_tunnel_connectors_ready 3`) {
		t.Fatalf("metrics output missing tunnel connectors_ready gauge: %s", body)
	}
}

// M5.5 backend-ha-leader-election spec § 8.1: pod_name-labelled leader metrics.
func TestM5_5LeaderMetricsExpose(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetLeaderStatus("ops-server-7b9d-abc12", true)
	metrics.ObserveLeaderHandover("ops-server-7b9d-abc12")
	metrics.ObserveLeaseRenew("ops-server-7b9d-abc12", "acquired")
	metrics.ObserveLeaseRenew("ops-server-7b9d-abc12", "lost")
	metrics.ObserveLeaseRenew("ops-server-7b9d-abc12", "slow_acquire")

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_leader_status{pod_name="ops-server-7b9d-abc12"} 1`) {
		t.Fatalf("metrics output missing leader_status gauge: %s", body)
	}
	if !strings.Contains(body, `zeroops_leader_handover_total{pod_name="ops-server-7b9d-abc12"} 1`) {
		t.Fatalf("metrics output missing leader_handover_total counter: %s", body)
	}
	if !strings.Contains(body, `zeroops_leader_lease_renew_total{outcome="acquired",pod_name="ops-server-7b9d-abc12"} 1`) {
		t.Fatalf("metrics output missing lease_renew_total acquired: %s", body)
	}
	if !strings.Contains(body, `zeroops_leader_lease_renew_total{outcome="lost",pod_name="ops-server-7b9d-abc12"} 1`) {
		t.Fatalf("metrics output missing lease_renew_total lost: %s", body)
	}
	if !strings.Contains(body, `zeroops_leader_lease_renew_total{outcome="slow_acquire",pod_name="ops-server-7b9d-abc12"} 1`) {
		t.Fatalf("metrics output missing lease_renew_total slow_acquire: %s", body)
	}
}

func TestM5_5SetLeaderStatusFalseClearsGauge(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetLeaderStatus("ops-server-7b9d-abc12", true)
	metrics.SetLeaderStatus("ops-server-7b9d-abc12", false)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `zeroops_leader_status{pod_name="ops-server-7b9d-abc12"} 0`) {
		t.Fatalf("leader_status must reset to 0 after SetLeaderStatus(false): %s", body)
	}
}

// T21 upload pipeline metrics tests.

func TestObserveUploadSuccess(t *testing.T) {
	m := NewMetrics()
	m.ObserveUploadSuccess(1024*1024, 3*time.Second)

	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()

	if !strings.Contains(body, `app_source_upload_total{reject_reason="",result="success"} 1`) {
		t.Errorf("upload_total success counter missing: %s", body)
	}
	if !strings.Contains(body, "app_source_upload_size_bytes_count 1") {
		t.Errorf("upload_size_bytes histogram count missing: %s", body)
	}
	if !strings.Contains(body, "app_source_upload_duration_seconds_count 1") {
		t.Errorf("upload_duration_seconds histogram count missing: %s", body)
	}
}

func TestObserveUploadRejection(t *testing.T) {
	m := NewMetrics()
	m.ObserveUploadRejection("sha256_mismatch")
	m.ObserveUploadRejection("archive_corrupt")

	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()

	if !strings.Contains(body, `app_source_upload_total{reject_reason="sha256_mismatch",result="rejected"} 1`) {
		t.Errorf("upload_total sha256_mismatch rejection missing: %s", body)
	}
	if !strings.Contains(body, `app_source_upload_total{reject_reason="archive_corrupt",result="rejected"} 1`) {
		t.Errorf("upload_total archive_corrupt rejection missing: %s", body)
	}
}

func TestObserveQuotaRejection(t *testing.T) {
	m := NewMetrics()
	m.ObserveQuotaRejection("pinned")
	m.ObserveQuotaRejection("daily")

	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()

	// quota-specific counter
	if !strings.Contains(body, `app_source_quota_rejection_total{reason="pinned"} 1`) {
		t.Errorf("quota_rejection_total pinned missing: %s", body)
	}
	if !strings.Contains(body, `app_source_quota_rejection_total{reason="daily"} 1`) {
		t.Errorf("quota_rejection_total daily missing: %s", body)
	}
	// also bumps the unified upload total with quota_ prefix
	if !strings.Contains(body, `app_source_upload_total{reject_reason="quota_pinned",result="rejected"} 1`) {
		t.Errorf("upload_total quota_pinned rejection missing: %s", body)
	}
	if !strings.Contains(body, `app_source_upload_total{reject_reason="quota_daily",result="rejected"} 1`) {
		t.Errorf("upload_total quota_daily rejection missing: %s", body)
	}
}

func TestObserveUploadGC(t *testing.T) {
	m := NewMetrics()
	m.ObserveUploadGC(5, 2)

	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()

	if !strings.Contains(body, `app_source_gc_deleted_total{outcome="success"} 5`) {
		t.Errorf("gc_deleted_total success=5 missing: %s", body)
	}
	if !strings.Contains(body, `app_source_gc_deleted_total{outcome="failure"} 2`) {
		t.Errorf("gc_deleted_total failure=2 missing: %s", body)
	}
}

func TestObserveUploadGCZeroDoesNotCreateSeries(t *testing.T) {
	m := NewMetrics()
	m.ObserveUploadGC(0, 0) // no-op: both counters should not appear

	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()

	// The metric help line exists but no labelled series should be present.
	if strings.Contains(body, `app_source_gc_deleted_total{`) {
		t.Errorf("gc_deleted_total should have no labelled series for zero counts: %s", body)
	}
}

func TestObserveArchiveDownload(t *testing.T) {
	m := NewMetrics()
	m.ObserveArchiveDownload("success")
	m.ObserveArchiveDownload("failure")
	m.ObserveArchiveDownload("success")

	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()

	if !strings.Contains(body, `app_source_archive_downloaded_total{outcome="success"} 2`) {
		t.Errorf("archive_downloaded_total success=2 missing: %s", body)
	}
	if !strings.Contains(body, `app_source_archive_downloaded_total{outcome="failure"} 1`) {
		t.Errorf("archive_downloaded_total failure=1 missing: %s", body)
	}
}
