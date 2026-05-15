package cloudflaretunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each invariant in this map is a substring that must appear verbatim
// in the named template file. Substring matching is intentional — the
// chart relies on Helm rendering, but the spec's hard rules
// (winshare-subdomain-and-tunnel § 16) are token-level requirements that
// remain stable across rendering.
var requiredSubstrings = map[string][]string{
	"Chart.yaml": {
		"name: cloudflare-tunnel",
		"appVersion: \"2025.1.0\"",
	},
	"values.yaml": {
		"replicaCount: 3",
		"tag: \"2025.1.0\"",
		"namespace: cloudflare-tunnel",
	},
	"templates/deployment.yaml": {
		"replicas: {{ .Values.replicaCount }}",
		"--no-autoupdate",
		"secretKeyRef",
		"runAsNonRoot: true",
	},
	"templates/networkpolicy.yaml": {
		"policyTypes:",
		"ingress: []",
		"port: 7844",
		"namespaceSelector",
	},
	"templates/namespace.yaml": {
		"pod-security.kubernetes.io/enforce",
		"pod-security.kubernetes.io/warn",
	},
	"templates/secret.yaml": {
		"name: cloudflared-tunnel-token",
	},
}

func TestChartFilesEnforceHardRules(t *testing.T) {
	for file, substrings := range requiredSubstrings {
		data, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		content := string(data)
		for _, sub := range substrings {
			if !strings.Contains(content, sub) {
				t.Errorf("%s: missing required substring %q", file, sub)
			}
		}
	}
}

func TestDeploymentGuardsReplicaFloor(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean("templates/deployment.yaml"))
	if err != nil {
		t.Fatalf("read deployment.yaml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "lt (int .Values.replicaCount) 3") {
		t.Errorf("deployment template missing replicaCount floor guard (spec § 16 #3)")
	}
	if !strings.Contains(content, "{{- fail \"cloudflare-tunnel chart requires replicaCount >= 3") {
		t.Errorf("deployment template missing explicit fail message for replicaCount floor")
	}
}
