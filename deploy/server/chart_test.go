package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// production-deployment spec § 6 / backend-ha spec § 14：substring 守 hard rules。
// Helm 渲染本身在 install 時行為決定；本 test 守住模板原始碼穿越 render 仍會保留的 token。
var requiredSubstrings = map[string][]string{
	"Chart.yaml": {
		"name: ops-server",
	},
	"values.yaml": {
		"replicas: 2",
		"mode: lease",
		"sleepSeconds: 5",
		"ingress:",
		"className: traefik",
		"config:",
		"domainBase: \"winshare.tw\"",
		"secretRef:",
		"name: ops-server-env",
	},
	"templates/deployment.yaml": {
		// backend-ha-leader-election § 14 hard rules
		"lt (int .Values.replicas) 2",
		"leaderElection.mode \"lease\"",
		"lt (int .Values.preStop.sleepSeconds) 5",
		// production-deployment spec § 6：envFrom configmap + secret
		"configMapRef:",
		"name: ops-server-config",
		"secretRef:",
		".Values.secretRef.name",
	},
	"templates/ingress.yaml": {
		// production-deployment spec § 6 hard rules
		"if not .Values.ingress.host",
		"ops-server chart requires ingress.host to be set",
		"ne .Values.ingress.className \"traefik\"",
		"kind: Ingress",
		"name: ops-server",
	},
	"templates/configmap.yaml": {
		// production-deployment spec § 6 hard rule
		"if not .Values.config.publicURL",
		"ops-server chart requires config.publicURL",
		"OPS_API_PUBLIC_URL:",
		"OPS_DOMAIN_BASE:",
		"OPS_GITOPS_REPO:",
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

// production-deployment spec § 10：本 chart 不可渲明文 Secret，
// 以避免任何 commit 路徑誤帶 secret 入 git。
func TestChartHasNoPlaintextSecretTemplate(t *testing.T) {
	matches, err := filepath.Glob("templates/secret*.yaml")
	if err != nil {
		t.Fatalf("glob secret templates: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("ops-server chart must not ship Secret templates "+
			"(production-deployment spec § 10): found %v", matches)
	}
}
