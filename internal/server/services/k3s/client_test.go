package k3s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	dynamicfake "k8s.io/client-go/dynamic/fake"
)

var (
	testNamespacesGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	testQuotaGVR      = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"}
	testLimitRangeGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "limitranges"}
	testPolicyGVR     = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
	testSecretGVR     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
)

func newFakeClient() *Client {
	scheme := runtime.NewScheme()
	return &Client{
		config:        &Config{},
		dynamicClient: dynamicfake.NewSimpleDynamicClient(scheme),
	}
}

func TestNewClient_DisableNamespaceIsolation_ReturnsClient(t *testing.T) {
	t.Setenv("KUBECONFIG", "")

	client, err := NewClient(&Config{DisableNamespaceIsolation: true})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned nil client")
	}
}

func TestNewClient_InvalidKubeconfigPath_ReturnsError(t *testing.T) {
	client, err := NewClient(&Config{
		KubeconfigPath:            "/path/that/does/not/exist",
		DisableNamespaceIsolation: false,
	})
	if err == nil {
		t.Fatalf("NewClient() error = nil, client=%v; want error", client)
	}
}

func TestEnsureNamespaceCreatesTeamNamespace(t *testing.T) {
	client := newFakeClient()

	namespace, err := client.EnsureNamespace(context.Background(), "team-1", "acme", "free")
	if err != nil {
		t.Fatalf("EnsureNamespace() error = %v", err)
	}
	if namespace != "team-acme" {
		t.Fatalf("EnsureNamespace() namespace = %q, want %q", namespace, "team-acme")
	}

	obj, err := client.dynamicClient.Resource(testNamespacesGVR).Get(context.Background(), "team-acme", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get namespace error = %v", err)
	}
	labels := obj.GetLabels()
	if labels["app.0ops.io/team-slug"] != "acme" || labels["app.0ops.io/plan"] != "free" || labels["app.0ops.io/team-id"] != "team-1" {
		t.Fatalf("namespace labels = %#v", labels)
	}
	// Spec § 4.2 + hard rule #4: PSA labels must be set at namespace creation.
	if labels["pod-security.kubernetes.io/enforce"] != "baseline" ||
		labels["pod-security.kubernetes.io/warn"] != "restricted" ||
		labels["pod-security.kubernetes.io/audit"] != "restricted" {
		t.Fatalf("PSA labels missing at creation: %#v", labels)
	}
}

func TestEnsureResourceQuotaCreatesTierQuota(t *testing.T) {
	client := newFakeClient()
	_, _ = client.EnsureNamespace(context.Background(), "team-1", "acme", "starter")

	if err := client.EnsureResourceQuota(context.Background(), "team-acme", "starter"); err != nil {
		t.Fatalf("EnsureResourceQuota() error = %v", err)
	}

	obj, err := client.dynamicClient.Resource(testQuotaGVR).Namespace("team-acme").Get(context.Background(), "default", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get ResourceQuota error = %v", err)
	}
	hard, found, err := unstructured.NestedStringMap(obj.Object, "spec", "hard")
	if err != nil || !found {
		t.Fatalf("ResourceQuota hard not found: found=%v err=%v", found, err)
	}
	if hard["requests.cpu"] != "4" || hard["requests.memory"] != "8Gi" || hard["pods"] != "30" {
		t.Fatalf("ResourceQuota hard = %#v", hard)
	}
}

func TestEnsureLimitRangeCreatesDefaultLimits(t *testing.T) {
	client := newFakeClient()

	if err := client.EnsureLimitRange(context.Background(), "team-acme"); err != nil {
		t.Fatalf("EnsureLimitRange() error = %v", err)
	}

	obj, err := client.dynamicClient.Resource(testLimitRangeGVR).Namespace("team-acme").Get(context.Background(), "default", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get LimitRange error = %v", err)
	}
	items, found, err := unstructured.NestedSlice(obj.Object, "spec", "limits")
	if err != nil || !found || len(items) == 0 {
		t.Fatalf("LimitRange limits invalid: found=%v len=%d err=%v", found, len(items), err)
	}
}

func TestEnsureNetworkPolicyCreatesIngressAndEgress(t *testing.T) {
	client := newFakeClient()

	if err := client.EnsureNetworkPolicy(context.Background(), "team-acme"); err != nil {
		t.Fatalf("EnsureNetworkPolicy() error = %v", err)
	}

	if _, err := client.dynamicClient.Resource(testPolicyGVR).Namespace("team-acme").Get(context.Background(), "default-deny-ingress", metav1.GetOptions{}); err != nil {
		t.Fatalf("ingress policy missing: %v", err)
	}
	if _, err := client.dynamicClient.Resource(testPolicyGVR).Namespace("team-acme").Get(context.Background(), "default-egress", metav1.GetOptions{}); err != nil {
		t.Fatalf("egress policy missing: %v", err)
	}
}

func TestPatchNamespacePSAAddsLabels(t *testing.T) {
	client := newFakeClient()
	_, _ = client.EnsureNamespace(context.Background(), "team-1", "acme", "free")

	if err := client.PatchNamespacePSA(context.Background(), "team-acme"); err != nil {
		t.Fatalf("PatchNamespacePSA() error = %v", err)
	}

	obj, err := client.dynamicClient.Resource(testNamespacesGVR).Get(context.Background(), "team-acme", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get namespace error = %v", err)
	}
	labels := obj.GetLabels()
	if labels["pod-security.kubernetes.io/enforce"] != "baseline" ||
		labels["pod-security.kubernetes.io/warn"] != "restricted" ||
		labels["pod-security.kubernetes.io/audit"] != "restricted" {
		t.Fatalf("PSA labels = %#v", labels)
	}
}

func TestEnsureTeamIsolationProvisionsAllPrimitives(t *testing.T) {
	client := newFakeClient()

	namespace, err := client.EnsureTeamIsolation(context.Background(), "team-1", "acme", "free")
	if err != nil {
		t.Fatalf("EnsureTeamIsolation() error = %v", err)
	}
	if namespace != "team-acme" {
		t.Fatalf("EnsureTeamIsolation() namespace = %q, want team-acme", namespace)
	}

	ns, err := client.dynamicClient.Resource(testNamespacesGVR).Get(context.Background(), "team-acme", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get namespace error = %v", err)
	}
	if ns.GetLabels()["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Fatalf("PSA enforce missing on namespace: %#v", ns.GetLabels())
	}
	if _, err := client.dynamicClient.Resource(testQuotaGVR).Namespace("team-acme").Get(context.Background(), "default", metav1.GetOptions{}); err != nil {
		t.Fatalf("ResourceQuota missing after EnsureTeamIsolation: %v", err)
	}
	if _, err := client.dynamicClient.Resource(testLimitRangeGVR).Namespace("team-acme").Get(context.Background(), "default", metav1.GetOptions{}); err != nil {
		t.Fatalf("LimitRange missing after EnsureTeamIsolation: %v", err)
	}
	if _, err := client.dynamicClient.Resource(testPolicyGVR).Namespace("team-acme").Get(context.Background(), "default-deny-ingress", metav1.GetOptions{}); err != nil {
		t.Fatalf("Ingress NetworkPolicy missing: %v", err)
	}
	if _, err := client.dynamicClient.Resource(testPolicyGVR).Namespace("team-acme").Get(context.Background(), "default-egress", metav1.GetOptions{}); err != nil {
		t.Fatalf("Egress NetworkPolicy missing: %v", err)
	}
}

func TestEnsureTeamIsolationIsIdempotent(t *testing.T) {
	client := newFakeClient()
	if _, err := client.EnsureTeamIsolation(context.Background(), "team-1", "acme", "starter"); err != nil {
		t.Fatalf("first EnsureTeamIsolation: %v", err)
	}
	if _, err := client.EnsureTeamIsolation(context.Background(), "team-1", "acme", "starter"); err != nil {
		t.Fatalf("second EnsureTeamIsolation: %v", err)
	}
}

func TestEnsureTeamIsolationRollsBackNamespaceWhenQuotaFails(t *testing.T) {
	client := newFakeClient()
	tracker := client.dynamicClient.(*dynamicfake.FakeDynamicClient).Tracker()
	sentinel := errors.New("quota apply failed")
	fake := client.dynamicClient.(*dynamicfake.FakeDynamicClient)
	fake.PrependReactor("create", "resourcequotas", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, sentinel
	})

	_, err := client.EnsureTeamIsolation(context.Background(), "team-1", "acme", "free")
	if err == nil {
		t.Fatal("expected EnsureTeamIsolation to fail when quota apply fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain missing sentinel: %v", err)
	}

	_, getErr := tracker.Get(testNamespacesGVR, "", "team-acme")
	if getErr == nil {
		t.Fatal("expected namespace to be rolled back when quota apply fails")
	}
	if !apierrors.IsNotFound(getErr) {
		t.Fatalf("expected NotFound after rollback, got %v", getErr)
	}
}

func TestEnsureTeamIsolationDisabledIsNoOp(t *testing.T) {
	client, err := NewClient(&Config{DisableNamespaceIsolation: true})
	if err != nil {
		t.Fatalf("NewClient(): %v", err)
	}
	ns, err := client.EnsureTeamIsolation(context.Background(), "team-1", "acme", "free")
	if err != nil {
		t.Fatalf("EnsureTeamIsolation() error = %v", err)
	}
	if ns != "team-acme" {
		t.Fatalf("ns = %q, want team-acme", ns)
	}
}

func TestPatchGHCRImagePullSecretCreatesDockerConfig(t *testing.T) {
	client := newFakeClient()
	_, _ = client.EnsureNamespace(context.Background(), "team-1", "acme", "free")

	dockerConfig := `{"auths":{"ghcr.io":{"auth":"dGVzdDp0ZXN0"}}}`
	if err := client.PatchGHCRImagePullSecret(context.Background(), "team-acme", dockerConfig); err != nil {
		t.Fatalf("PatchGHCRImagePullSecret() error = %v", err)
	}

	obj, err := client.dynamicClient.Resource(testSecretGVR).Namespace("team-acme").Get(context.Background(), "ghcr-pull", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get secret error = %v", err)
	}
	typ, _, _ := unstructured.NestedString(obj.Object, "type")
	if typ != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("secret type = %q", typ)
	}
	encoded, found, err := unstructured.NestedString(obj.Object, "data", ".dockerconfigjson")
	if err != nil || !found {
		t.Fatalf("secret data missing: found=%v err=%v", found, err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode dockerconfigjson: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("invalid dockerconfigjson payload: %v", err)
	}
}
