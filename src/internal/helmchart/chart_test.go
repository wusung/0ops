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
		// audit_log 分割輪替 CronJob 的開關與視窗長度
		"auditRollover:",
		"lookaheadMonths:",
		// Metering on by default, but switchable (drops the ClusterRole)
		"usage:",
		"enabled: true",
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
	// resource-usage-metering spec § 10 — allocation ledger needs to
	// observe managed pods across the dynamically created team
	// namespaces, so the grant has to be cluster-scoped. It must stay
	// read-only: the ledger never creates or mutates a pod.
	"templates/clusterrole.yaml": {
		"kind: ClusterRole",
		// The widest grant this backend holds must be opt-out.
		"if .Values.usage.enabled",
		"resources: [\"pods\"]",
		"- list",
		"- watch",
		// Observed-usage track (spec § 8). Read-only, and the ledger
		// stays correct without it.
		"metrics.k8s.io",
	},
	// k3s-namespace-isolation spec § 9.1 — EnsureTeamIsolation writes
	// Namespace / ResourceQuota / LimitRange / NetworkPolicy / Secret in
	// dynamically created team namespaces, so the grant must be
	// cluster-scoped and must actually exist in the chart (issue #163).
	"templates/clusterrole-provisioner.yaml": {
		"kind: ClusterRole",
		"resources: [\"namespaces\"]",
		"resources: [\"resourcequotas\", \"limitranges\", \"secrets\"]",
		"resources: [\"networkpolicies\"]",
		"- create",
		"- update",
		"- delete",
	},
	"templates/clusterrolebinding-provisioner.yaml": {
		"kind: ClusterRoleBinding",
		"kind: ServiceAccount",
		"kind: ClusterRole",
		"-provisioner",
	},
	"templates/clusterrolebinding.yaml": {
		"kind: ClusterRoleBinding",
		"if .Values.usage.enabled",
		"kind: ServiceAccount",
		"kind: ClusterRole",
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
	// audit_log partition rollover：00007 的固定視窗在 2026-09-01 用盡、
	// 稽核寫入全數失敗，因為 audit.Rollover 從未被呼叫。本 CronJob 是那個
	// 缺席的呼叫者。
	"templates/cronjob-audit-rollover.yaml": {
		"kind: CronJob",
		// DDL 必須用 privileged 憑證；migration 00014 明令不可用 "0ops_app"。
		"key: DATABASE_URL",
		"secretKeyRef:",
		// 與 server 共用映像，故 CronJob 必須覆寫 command，否則會跑起 server。
		"command: [\"/usr/local/bin/0ops-audit-rollover\"]",
		// 重疊執行會讓兩個 CREATE 互撞。
		"concurrencyPolicy: Forbid",
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

// TestUsageReaderClusterRoleIsReadOnly — resource-usage-metering spec
// § 14 rule #2 relies on the ledger only ever reading pod objects. A
// cluster-scoped grant is the widest permission this backend holds, so
// any write verb sneaking in must break the build rather than a review.
func TestUsageReaderClusterRoleIsReadOnly(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(chartDir, "templates", "clusterrole.yaml"))
	if err != nil {
		t.Fatalf("read clusterrole.yaml: %v", err)
	}
	content := string(data)
	for _, forbidden := range []string{
		"- create",
		"- update",
		"- patch",
		"- delete",
		"- deletecollection",
		`"*"`,
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("usage-reader ClusterRole must stay read-only; found %q", forbidden)
		}
	}
	// The grant must not widen beyond pods, in either API group.
	for _, resource := range []string{"secrets", "nodes", "configmaps", "namespaces"} {
		if strings.Contains(content, `"`+resource+`"`) {
			t.Errorf("usage-reader ClusterRole must stay scoped to pods; found %q", resource)
		}
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

// TestProvisionerClusterRoleGrantsNoBroadReads — the provisioner grant is a
// write grant scoped to what EnsureTeamIsolation actually calls. It must
// never pick up list/watch: a SA that can list secrets cluster-wide reads
// every team's registry credentials, a different blast radius from
// creating one secret in one namespace.
func TestProvisionerClusterRoleGrantsNoBroadReads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(chartDir, "templates", "clusterrole-provisioner.yaml"))
	if err != nil {
		t.Fatalf("read clusterrole-provisioner.yaml: %v", err)
	}
	body := string(data)
	idx := strings.Index(body, "rules:")
	if idx < 0 {
		t.Fatalf("clusterrole-provisioner.yaml has no rules block")
	}
	rules := body[idx:]
	for _, forbidden := range []string{"- list", "- watch", "- deletecollection", `"*"`} {
		if strings.Contains(rules, forbidden) {
			t.Errorf("provisioner ClusterRole must not grant %q", forbidden)
		}
	}
}
