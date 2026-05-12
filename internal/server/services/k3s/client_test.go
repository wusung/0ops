package k3s

import (
	"context"
	"testing"
)

func TestEnsureResourceQuotaRejectsUnknownPlan(t *testing.T) {
	client, err := NewClient(&Config{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if err := client.EnsureResourceQuota(context.Background(), "team-acme", "unknown"); err == nil {
		t.Fatalf("EnsureResourceQuota() error = nil, want non-nil for unknown plan")
	}
}

func TestNamespaceIsolationPlanArtifacts(t *testing.T) {
	client, err := NewClient(&Config{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ns, err := client.EnsureNamespace(context.Background(), "team-1", "acme", "free")
	if err != nil {
		t.Fatalf("EnsureNamespace() error = %v", err)
	}
	if ns != "team-acme" {
		t.Fatalf("EnsureNamespace() = %q, want team-acme", ns)
	}
	if err := client.EnsureResourceQuota(context.Background(), ns, "free"); err != nil {
		t.Fatalf("EnsureResourceQuota() error = %v", err)
	}
	if err := client.EnsureLimitRange(context.Background(), ns); err != nil {
		t.Fatalf("EnsureLimitRange() error = %v", err)
	}
	if err := client.EnsureNetworkPolicy(context.Background(), ns); err != nil {
		t.Fatalf("EnsureNetworkPolicy() error = %v", err)
	}
	if err := client.PatchNamespacePSA(context.Background(), ns); err != nil {
		t.Fatalf("PatchNamespacePSA() error = %v", err)
	}

	got, err := client.GetNamespace(context.Background(), ns)
	if err != nil {
		t.Fatalf("GetNamespace() error = %v", err)
	}
	if got == nil {
		t.Fatalf("GetNamespace() = nil, want namespace details")
	}
	if _, ok := got["resource_quota"]; !ok {
		t.Fatalf("resource_quota missing from namespace details: %#v", got)
	}
	if _, ok := got["limit_range"]; !ok {
		t.Fatalf("limit_range missing from namespace details: %#v", got)
	}
	if _, ok := got["network_policy"]; !ok {
		t.Fatalf("network_policy missing from namespace details: %#v", got)
	}
	if _, ok := got["psa"]; !ok {
		t.Fatalf("psa missing from namespace details: %#v", got)
	}
}
