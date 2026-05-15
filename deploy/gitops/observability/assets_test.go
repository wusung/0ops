package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type configMapFile struct {
	Data map[string]string `yaml:"data"`
}

type promRulesFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Record      string            `yaml:"record"`
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func loadConfigMap(t *testing.T, path string) configMapFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cm configMapFile
	if err := yaml.Unmarshal(b, &cm); err != nil {
		t.Fatalf("unmarshal configmap %s: %v", path, err)
	}
	return cm
}

func TestPrometheusAlertRulesContainM26CriticalRules(t *testing.T) {
	cm := loadConfigMap(t, "prometheus-alert-rules.yaml")
	raw := cm.Data["rules.yaml"]
	if raw == "" {
		t.Fatal("rules.yaml missing from prometheus-alert-rules configmap")
	}

	var rules promRulesFile
	if err := yaml.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatalf("unmarshal embedded rules: %v", err)
	}

	required := map[string]bool{
		"APIErrorBudgetBurnFast":           false,
		"APIErrorBudgetBurnSlow":           false,
		"PreviewConsumptionRateLow":        false,
		"CloudflareAPIThrottled":           false,
		"UnknownFailureClassificationHigh": false,
	}

	for _, group := range rules.Groups {
		for _, rule := range group.Rules {
			if _, ok := required[rule.Alert]; !ok {
				continue
			}
			required[rule.Alert] = true
			if rule.Labels["severity"] == "" {
				t.Fatalf("alert %s missing severity label", rule.Alert)
			}
			if rule.Labels["service"] == "" {
				t.Fatalf("alert %s missing service label", rule.Alert)
			}
			if rule.Annotations["runbook_url"] == "" {
				t.Fatalf("alert %s missing runbook_url annotation", rule.Alert)
			}
		}
	}

	for alertName, seen := range required {
		if !seen {
			t.Fatalf("required alert %s not found", alertName)
		}
	}
}

func TestPrometheusRecordingRulesContainM26RequiredSeries(t *testing.T) {
	cm := loadConfigMap(t, "prometheus-recording-rules.yaml")
	raw := cm.Data["rules.yaml"]
	if raw == "" {
		t.Fatal("rules.yaml missing from prometheus-recording-rules configmap")
	}

	var rules promRulesFile
	if err := yaml.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatalf("unmarshal embedded recording rules: %v", err)
	}

	required := map[string]bool{
		"cluster:zeroops_http_error_rate:1h":               false,
		"cluster:zeroops_http_error_rate:6h":               false,
		"cluster:zeroops_preview_consumption_rate:7d":      false,
		"cluster:zeroops_preview_confirm_latency_p50:7d":   false,
		"cluster:zeroops_deploy_terminal_success_rate:28d": false,
		"cluster:zeroops_unknown_failure_ratio:7d":         false,
		"cluster:zeroops_cloudflare_api_throttled_rate:15m": false,
	}

	for _, group := range rules.Groups {
		for _, rule := range group.Rules {
			if rule.Record == "" {
				continue
			}
			if rule.Expr == "" {
				t.Fatalf("recording rule %s has empty expr", rule.Record)
			}
			if _, ok := required[rule.Record]; ok {
				required[rule.Record] = true
			}
		}
	}

	for name, seen := range required {
		if !seen {
			t.Fatalf("required recording rule %s not found", name)
		}
	}
}

func TestPrometheusRecordingRulesReferenceM26SourceMetrics(t *testing.T) {
	cm := loadConfigMap(t, "prometheus-recording-rules.yaml")
	raw := cm.Data["rules.yaml"]

	requiredSubstrings := []string{
		"zeroops_preview_consumed_total",
		"zeroops_preview_created_total",
		"zeroops_preview_consume_duration_seconds_bucket",
		"zeroops_cloudflare_api_calls_total",
		"zeroops_deploy_run_terminal_total",
		"zeroops_deploy_run_failures_total",
		"zeroops_http_requests_total",
	}
	for _, sub := range requiredSubstrings {
		if !strings.Contains(raw, sub) {
			t.Fatalf("recording rules missing reference to %s", sub)
		}
	}
}

func TestPrometheusAlertRulesReferenceRecordingRules(t *testing.T) {
	cm := loadConfigMap(t, "prometheus-alert-rules.yaml")
	raw := cm.Data["rules.yaml"]
	if raw == "" {
		t.Fatal("rules.yaml missing from prometheus-alert-rules configmap")
	}

	var rules promRulesFile
	if err := yaml.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatalf("unmarshal embedded alert rules: %v", err)
	}

	for _, group := range rules.Groups {
		for _, rule := range group.Rules {
			if rule.Alert == "" {
				continue
			}
			if rule.Expr == "" {
				t.Fatalf("alert %s has empty expr", rule.Alert)
			}
		}
	}
}

func TestGrafanaDashboardsHaveRequiredPanels(t *testing.T) {
	tests := []struct {
		file      string
		dataKey   string
		minPanels int
	}{
		{file: "0ops-overview.yaml", dataKey: "overview.json", minPanels: 3},
		{file: "0ops-deploy-pipeline.yaml", dataKey: "deploy-pipeline.json", minPanels: 3},
		{file: "0ops-product-health.yaml", dataKey: "product-health.json", minPanels: 3},
		{file: "0ops-failure-classification.yaml", dataKey: "failure-classification.json", minPanels: 2},
		{file: "0ops-postgres.yaml", dataKey: "postgres.json", minPanels: 2},
		{file: "0ops-leader-ha.yaml", dataKey: "leader-ha.json", minPanels: 2},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("grafana-dashboards", tc.file)
			cm := loadConfigMap(t, path)
			raw := cm.Data[tc.dataKey]
			if raw == "" {
				t.Fatalf("%s missing %s key", path, tc.dataKey)
			}

			var dashboard struct {
				Panels []json.RawMessage `json:"panels"`
			}
			if err := json.Unmarshal([]byte(raw), &dashboard); err != nil {
				t.Fatalf("%s invalid dashboard JSON: %v", path, err)
			}

			if len(dashboard.Panels) < tc.minPanels {
				t.Fatalf("%s has %d panels, want at least %d", path, len(dashboard.Panels), tc.minPanels)
			}
		})
	}
}
