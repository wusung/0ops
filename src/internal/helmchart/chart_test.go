package helmchart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chartDir points at the Helm chart under the repo root. The Go module
// lives at src/, so from this package (src/internal/helmchart) the chart
// sits three directories up.
const chartDir = "../../../deploy/server"

// Spec § 14 hard rules + § 7 deployment shape are translated into
// substring assertions against the raw template files. Helm render-time
// `fail` calls are exercised by TestTemplateGuards.
var requiredSubstrings = map[string][]string{
	"Chart.yaml": {
		"name: ops-server",
		"appVersion:",
	},
	"values.yaml": {
		// Hard rule #1 — replicas default 2
		"replicas: 2",
		// Hard rule #1 / #5 — lease mode default
		"mode: lease",
		// Spec § 7.1 — strategy
		"maxSurge: 1",
		"maxUnavailable: 0",
		// Spec § 7.1 — grace period 60s
		"terminationGracePeriodSeconds: 60",
		// Hard rule #7 — preStop sleep 5
		"sleepSeconds: 5",
		// Lease metadata defaults
		"leaseName: 0ops-backend-leader",
		"namespace: system-0ops",
	},
	"templates/deployment.yaml": {
		"kind: Deployment",
		"replicas: {{ .Values.replicas }}",
		"type: RollingUpdate",
		"maxSurge: {{ .Values.strategy.maxSurge }}",
		"maxUnavailable: {{ .Values.strategy.maxUnavailable }}",
		"terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds }}",
		// Hard rule #7 — preStop must contain sleep
		"sleep",
		"preStop:",
		"readinessProbe:",
		"livenessProbe:",
		".Values.probes.readiness.path",
		".Values.probes.liveness.path",
		// OPS_LEADER_MODE wired to lease in production
		"name: OPS_LEADER_MODE",
		"value: {{ .Values.leaderElection.mode | quote }}",
		// POD_NAME from downward API → required for leader Identity
		"name: POD_NAME",
		"fieldPath: metadata.name",
		// ServiceAccount must be referenced
		"serviceAccountName:",
		// Hard rule #2 (spec § 14): single-replica gate via Helm fail
		"backend chart requires replicas",
	},
	"templates/service.yaml": {
		"kind: Service",
		".Values.service.port",
	},
	"templates/serviceaccount.yaml": {
		"kind: ServiceAccount",
	},
	"templates/role.yaml": {
		"kind: Role",
		"coordination.k8s.io",
		"leases",
		// resourceName must be narrowed to the single Lease object —
		// the backend SA must not be able to mutate other Leases.
		"resourceNames:",
		".Values.leaderElection.leaseName",
		"get",
		"watch",
		"update",
	},
	"templates/rolebinding.yaml": {
		"kind: RoleBinding",
		"kind: ServiceAccount",
		"kind: Role",
	},
	// production-deployment spec § 6（PR #107 曾落在 module 外的死測試檔，
	// 本檔為唯一活測試 — manage.sh test 只跑 src/ module）
	"templates/ingress.yaml": {
		"if not .Values.ingress.host",
		"ops-server chart requires ingress.host to be set",
		`ne .Values.ingress.className "traefik"`,
		"kind: Ingress",
	},
	"templates/configmap.yaml": {
		"if not .Values.config.publicURL",
		"ops-server chart requires config.publicURL",
		"OPS_API_PUBLIC_URL:",
		"OPS_DOMAIN_BASE:",
		"OPS_GITOPS_REPO:",
	},
	// K8s 部署路徑的 migrations：pre-install/pre-upgrade hook（ArgoCD 映射
	// PreSync），goose up 冪等。沒有這個 Job，server 對空 schema 啟動。
	"templates/migrate-job.yaml": {
		"helm.sh/hook: pre-install,pre-upgrade",
		"kind: Job",
		".Values.migrations.image.repository",
		// DATABASE_URL 一律取自 sealed secret，不渲明文
		"secretKeyRef:",
		"key: DATABASE_URL",
		"runAsNonRoot: true",
	},
}

// TestChartHasNoPlaintextSecretTemplate — production-deployment spec § 10：
// 本 chart 不可渲明文 Secret；cluster 內 Secret 一律由 sealed-secrets unseal。
func TestChartHasNoPlaintextSecretTemplate(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(chartDir, "templates", "secret*.yaml"))
	if err != nil {
		t.Fatalf("glob secret templates: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("ops-server chart must not ship Secret templates (spec § 10): %v", matches)
	}
}

// TestImageDefaultsPointAtPublishedRegistry 守 release workflow images job
// 與 chart 預設的對齊：ghcr.io/wusung/0ops-{server,migrations}（winshare
// namespace 從未發佈過，會 ImagePullBackOff）。
func TestImageDefaultsPointAtPublishedRegistry(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"repository: ghcr.io/wusung/0ops-server",
		"migrations:",
		"repository: ghcr.io/wusung/0ops-migrations",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("values.yaml missing %q", want)
		}
	}
	if strings.Contains(content, "ghcr.io/winshare/") {
		t.Errorf("values.yaml still references unpublished ghcr.io/winshare/ namespace")
	}
}

func TestChartFilesEnforceSpec(t *testing.T) {
	for file, substrings := range requiredSubstrings {
		data, err := os.ReadFile(filepath.Clean(filepath.Join(chartDir, file)))
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

// TestTemplateGuards asserts the Helm render-time `fail` calls that
// catch values mis-configurations before they reach a cluster.
func TestTemplateGuards(t *testing.T) {
	cases := []struct {
		file        string
		mustContain []string
	}{
		{
			file: "templates/deployment.yaml",
			mustContain: []string{
				// Hard rule #1: replicas < 2
				"lt (int .Values.replicas) 2",
				`spec § 14 hard rule #1`,
				// Hard rule #1 / #5: mode != lease
				`ne .Values.leaderElection.mode "lease"`,
				// Hard rule #7: preStop sleep < 5
				"lt (int .Values.preStop.sleepSeconds) 5",
				`spec § 14 hard rule #7`,
			},
		},
	}
	for _, c := range cases {
		data, err := os.ReadFile(filepath.Clean(filepath.Join(chartDir, c.file)))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		content := string(data)
		for _, sub := range c.mustContain {
			if !strings.Contains(content, sub) {
				t.Errorf("%s: missing required guard %q", c.file, sub)
			}
		}
	}
}

// TestValuesDefaultsMatchSpec ensures the chart ships with the
// production-required defaults (replicas=2, mode=lease, preStop=5,
// leaseName=0ops-backend-leader, terminationGracePeriodSeconds=60).
func TestValuesDefaultsMatchSpec(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"replicas: 2",
		"mode: lease",
		"leaseName: 0ops-backend-leader",
		"namespace: system-0ops",
		"sleepSeconds: 5",
		"terminationGracePeriodSeconds: 60",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("values.yaml missing default %q", want)
		}
	}
}
