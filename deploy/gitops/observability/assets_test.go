package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type configMapFile struct {
	Data map[string]string `yaml:"data"`
}

type promRulesFile struct {
	Groups []struct {
		Rules []struct {
			Alert       string            `yaml:"alert"`
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
