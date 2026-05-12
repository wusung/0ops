package server

import (
"net/http"
"net/http/httptest"
"strings"
"testing"
"time"

"github.com/winshare/zeroops/internal/server/observability"
)

func TestM2MetricsIntegration(t *testing.T) {
// Setup: Create metrics that track observations
metrics := observability.NewMetrics()

// Bind metrics to app handlers
BindCreateAppMetrics(
func(outcome string) {
metrics.ObserveCreateAppPreview(outcome)
},
func(outcome string, idempotentReplay bool) {
metrics.ObserveCreateAppConfirm(outcome, idempotentReplay)
},
)

BindM2Metrics(
func(teamBucket string) {
metrics.ObservePreviewCreated(teamBucket)
},
func(outcome, teamBucket string) {
metrics.ObservePreviewConsumed(outcome, teamBucket)
},
func(stateFrom, stateTo, teamBucket string) {
metrics.ObserveDeployRunTransition(stateFrom, stateTo, teamBucket)
},
func(outcome, teamBucket string, duration time.Duration) {
metrics.ObserveDeployRunLeadTime(outcome, teamBucket, duration)
},
)

// Step 1: Simulate preview creation
recordM2PreviewCreated("00")
recordCreateAppPreviewMetric("success")

// Step 2: Simulate preview consumption
recordM2PreviewConsumed("success", "00")
recordCreateAppConfirmMetric("success", false)

// Step 3: Simulate deploy state transitions
recordM2DeployRunTransition("queued", "preparing", "00")
recordM2DeployRunTransition("preparing", "building", "00")
recordM2DeployRunTransition("building", "deployed", "00")
recordM2DeployRunLeadTime("success", "00", 5*time.Minute)

// Step 4: Simulate Cloudflare tunnel metric
metrics.SetCloudflareConnectorsReady("us-west", 2)

// Scrape metrics endpoint
metricsRec := httptest.NewRecorder()
metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest("GET", "/metrics", nil))

body := metricsRec.Body.String()

// Verify M2 metrics are present
tests := []struct {
name   string
metric string
}{
{"preview_created", "zeroops_preview_created_total{team_bucket=\"00\"} 1"},
{"preview_consumed", "zeroops_preview_consumed_total{outcome=\"success\",team_bucket=\"00\"} 1"},
{"deploy_state_transitions", "zeroops_deploy_run_state_transitions_total{state_from=\"queued\",state_to=\"preparing\",team_bucket=\"00\"} 1"},
{"deploy_building", "zeroops_deploy_run_state_transitions_total{state_from=\"preparing\",state_to=\"building\",team_bucket=\"00\"} 1"},
{"deploy_deployed", "zeroops_deploy_run_state_transitions_total{state_from=\"building\",state_to=\"deployed\",team_bucket=\"00\"} 1"},
{"deploy_lead_time", "zeroops_deploy_run_lead_time_seconds_bucket"},
{"cloudflare_connectors", "zeroops_cloudflare_tunnel_connectors_ready{region=\"us-west\"} 2"},
{"create_app_previews", "zeroops_create_app_previews_total{outcome=\"success\"} 1"},
{"create_app_confirms", "zeroops_create_app_confirms_total{idempotent_replay=\"false\",outcome=\"success\"} 1"},
}

for _, tt := range tests {
if !strings.Contains(body, tt.metric) {
t.Errorf("metric %q not found. Expected substring: %s\nMetrics output: %s", tt.name, tt.metric, body[:1000])
}
}
}

func TestM2MetricsInPreviewCreateAppFlow(t *testing.T) {
// Integration test: full preview → confirm flow with metrics
metrics := observability.NewMetrics()

BindCreateAppMetrics(
func(outcome string) { metrics.ObserveCreateAppPreview(outcome) },
func(outcome string, idempotentReplay bool) { metrics.ObserveCreateAppConfirm(outcome, idempotentReplay) },
)

BindM2Metrics(
func(teamBucket string) { metrics.ObservePreviewCreated(teamBucket) },
func(outcome, teamBucket string) { metrics.ObservePreviewConsumed(outcome, teamBucket) },
func(stateFrom, stateTo, teamBucket string) { metrics.ObserveDeployRunTransition(stateFrom, stateTo, teamBucket) },
func(outcome, teamBucket string, duration time.Duration) { metrics.ObserveDeployRunLeadTime(outcome, teamBucket, duration) },
)

// Record metrics for flow
metrics.ObservePreviewCreated("00")
metrics.ObservePreviewConsumed("success", "00")
metrics.ObserveDeployRunTransition("unknown", "deployed", "00")
metrics.ObserveDeployRunLeadTime("success", "00", 45*time.Second)

// Verify metrics count
metricsRec := httptest.NewRecorder()
metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest("GET", "/metrics", nil))
body := metricsRec.Body.String()

// Check that metrics have the expected values
if !strings.Contains(body, `zeroops_preview_created_total{team_bucket="00"} 1`) {
t.Error("preview_created_total not found or incorrect value")
}

if !strings.Contains(body, `zeroops_preview_consumed_total{outcome="success",team_bucket="00"} 1`) {
t.Error("preview_consumed_total not found or incorrect value")
}

if !strings.Contains(body, `zeroops_deploy_run_state_transitions_total{state_from="unknown",state_to="deployed",team_bucket="00"} 1`) {
t.Error("deploy_run_state_transitions_total not found or incorrect value")
}

if !strings.Contains(body, `zeroops_deploy_run_lead_time_seconds_count{outcome="success",team_bucket="00"} 1`) {
t.Error("deploy_run_lead_time_seconds_count not found or incorrect value")
}
}

func TestM2MetricsRecordingRuleFormat(t *testing.T) {
// Verify metric format matches Prometheus conventions
metrics := observability.NewMetrics()

metrics.ObservePreviewCreated("ac")
metrics.ObservePreviewConsumed("success", "ac")
metrics.ObserveDeployRunTransition("queued", "preparing", "ac")
metrics.SetCloudflareConnectorsReady("eu-west-1", 3)

metricsRec := httptest.NewRecorder()
metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest("GET", "/metrics", nil))

if metricsRec.Code != http.StatusOK {
t.Fatalf("metrics handler returned %d, want 200", metricsRec.Code)
}

body := metricsRec.Body.String()

// Verify metric lines are valid Prometheus format: metric_name{labels} value
expectedPatterns := []string{
`zeroops_preview_created_total{team_bucket="ac"} 1`,
`zeroops_preview_consumed_total{outcome="success",team_bucket="ac"} 1`,
`zeroops_deploy_run_state_transitions_total{state_from="queued",state_to="preparing",team_bucket="ac"} 1`,
`zeroops_cloudflare_tunnel_connectors_ready{region="eu-west-1"} 3`,
}

for _, pattern := range expectedPatterns {
if !strings.Contains(body, pattern) {
t.Errorf("metric pattern not found: %s", pattern)
}
}
}
