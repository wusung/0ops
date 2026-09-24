package helmchart

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The provisioner ClusterRole is the one grant that decides whether the
// first create_app for a new team succeeds or 403s (issue #163). Asserting
// it with substrings would not catch the failure mode that produced the
// bug — a rule that looks right but does not cover a verb the code calls —
// so the rules are parsed and compared against what k3s.Client actually
// issues.
type clusterRole struct {
	Kind  string `yaml:"kind"`
	Rules []struct {
		APIGroups     []string `yaml:"apiGroups"`
		Resources     []string `yaml:"resources"`
		ResourceNames []string `yaml:"resourceNames"`
		Verbs         []string `yaml:"verbs"`
	} `yaml:"rules"`
}

var (
	helmComment = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)
	helmAction  = regexp.MustCompile(`\{\{.*?\}\}`)
)

// stripHelm turns a template into parseable YAML: the leading comment block
// and the standalone {{- if }} / {{- end }} lines go away, and inline
// actions collapse to a literal so metadata still unmarshals.
func stripHelm(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := helmComment.ReplaceAllString(string(data), "")
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{{") {
			continue
		}
		kept = append(kept, helmAction.ReplaceAllString(line, "rendered"))
	}
	return strings.Join(kept, "\n")
}

// grant keys one (apiGroup, resource, verb) triple to whether every rule
// carrying it was pinned to explicit resourceNames.
type grant struct{ group, resource, verb string }

func provisionerGrants(t *testing.T) map[grant]bool {
	t.Helper()
	var role clusterRole
	raw := stripHelm(t, filepath.Join(chartDir, "templates", "clusterrole-provisioner.yaml"))
	if err := yaml.Unmarshal([]byte(raw), &role); err != nil {
		t.Fatalf("parse clusterrole-provisioner.yaml: %v\n%s", err, raw)
	}
	if role.Kind != "ClusterRole" {
		t.Fatalf("expected a ClusterRole, got %q", role.Kind)
	}
	pinned := map[grant]bool{}
	for _, rule := range role.Rules {
		for _, g := range rule.APIGroups {
			for _, res := range rule.Resources {
				for _, verb := range rule.Verbs {
					k := grant{g, res, verb}
					named := len(rule.ResourceNames) > 0
					if prev, seen := pinned[k]; seen {
						named = named && prev
					}
					pinned[k] = named
				}
			}
		}
	}
	return pinned
}

// TestProvisionerClusterRoleCoversEveryClientCall pins the grant to the
// call sites in services/k3s/client.go. delete on namespaces is load
// bearing: EnsureTeamIsolation rolls the namespace back when a later step
// fails (k3s-namespace-isolation spec § 15 hard rule #3), and without the
// verb the rollback itself 403s.
func TestProvisionerClusterRoleCoversEveryClientCall(t *testing.T) {
	grants := provisionerGrants(t)
	required := []grant{
		{"", "namespaces", "get"},    // upsertResource / PatchNamespacePSA / GetNamespace
		{"", "namespaces", "create"}, // EnsureNamespace, first create_app for a team
		{"", "namespaces", "update"}, // EnsureNamespace re-apply + PatchNamespacePSA
		{"", "namespaces", "delete"}, // EnsureTeamIsolation rollback + DeleteNamespace

		{"", "resourcequotas", "get"},
		{"", "resourcequotas", "create"},
		{"", "resourcequotas", "update"},

		{"", "limitranges", "get"},
		{"", "limitranges", "create"},
		{"", "limitranges", "update"},

		{"networking.k8s.io", "networkpolicies", "get"},
		{"networking.k8s.io", "networkpolicies", "create"},
		{"networking.k8s.io", "networkpolicies", "update"},

		{"", "secrets", "get"}, // PatchGHCRImagePullSecret upsert
		{"", "secrets", "create"},
		{"", "secrets", "update"},
	}
	for _, want := range required {
		if _, ok := grants[want]; !ok {
			t.Errorf("provisioner ClusterRole never grants %s on %q (apiGroup %q); k3s.Client calls it",
				want.verb, want.resource, want.group)
		}
	}
}

// TestProvisionerClusterRoleStaysNarrow guards the other direction. A
// cluster-scoped grant that can read arbitrary secrets would hand the
// backend its own DATABASE_URL and the sealed-secrets key, so reads must
// stay pinned to the fixed object names client.go writes.
func TestProvisionerClusterRoleStaysNarrow(t *testing.T) {
	grants := provisionerGrants(t)

	// Reads that are not name-pinned. namespaces is the documented
	// exception: RBAC cannot express a `team-*` prefix.
	for g, pinned := range grants {
		if g.verb != "get" && g.verb != "list" && g.verb != "watch" {
			continue
		}
		if g.resource == "namespaces" {
			continue
		}
		if !pinned {
			t.Errorf("%s on %q is not restricted by resourceNames; a cluster-wide read of that resource is too wide",
				g.verb, g.resource)
		}
	}

	for g := range grants {
		switch g.verb {
		case "list", "watch", "deletecollection", "*":
			if g.resource != "namespaces" {
				t.Errorf("provisioner ClusterRole must not grant %s on %q; k3s.Client never calls it",
					g.verb, g.resource)
			}
		}
		if g.resource == "*" || g.group == "*" {
			t.Errorf("provisioner ClusterRole must not use wildcards; found %+v", g)
		}
		// delete anywhere but namespaces would let the backend remove
		// objects it only ever upserts.
		if g.verb == "delete" && g.resource != "namespaces" {
			t.Errorf("provisioner ClusterRole must not grant delete on %q", g.resource)
		}
	}

	// secrets reads must be pinned to the one object the backend owns.
	raw := stripHelm(t, filepath.Join(chartDir, "templates", "clusterrole-provisioner.yaml"))
	if !strings.Contains(raw, "ghcr-pull") {
		t.Error("secrets grant must be pinned to the ghcr-pull resourceName")
	}
}

// TestProvisionerGrantIsOptOut mirrors the usage-reader convention: the
// widest grants the chart ships are switchable, and the binding must be
// gated by the same value as the role so one cannot outlive the other.
func TestProvisionerGrantIsOptOut(t *testing.T) {
	for _, name := range []string{"clusterrole-provisioner.yaml", "clusterrolebinding-provisioner.yaml"} {
		data, err := os.ReadFile(filepath.Join(chartDir, "templates", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), "if .Values.namespaceProvisioning.enabled") {
			t.Errorf("%s must be gated on .Values.namespaceProvisioning.enabled", name)
		}
	}
}
