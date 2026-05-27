// Package k3s provides K3s cluster client and namespace management utilities.
package k3s

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Config holds K3s cluster connection parameters.
type Config struct {
	// KubeconfigPath is the path to kubeconfig file.
	// If empty, uses KUBECONFIG env var or default locations.
	KubeconfigPath string

	// APIServerURL is the K3s API server URL (e.g., https://127.0.0.1:6443).
	// If provided, overrides kubeconfig value.
	APIServerURL string

	// DisableNamespaceIsolation disables all K3s namespace operations.
	// Useful for development/testing without a cluster.
	DisableNamespaceIsolation bool
}

// Client wraps K3s cluster connection and namespace operations.
type Client struct {
	config        *Config
	dynamicClient dynamic.Interface
}

var (
	namespaceGVR     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	resourceQuotaGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "resourcequotas"}
	limitRangeGVR    = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "limitranges"}
	networkPolicyGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
	secretGVR        = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
)

// NewClient creates a new K3s client from kubeconfig or in-cluster config.
// If DisableNamespaceIsolation is true, returns a no-op client.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	c := &Client{config: cfg}
	if cfg.DisableNamespaceIsolation {
		return c, nil
	}

	restConfig, err := loadKubeConfig(cfg.KubeconfigPath, cfg.APIServerURL)
	if err != nil {
		return nil, fmt.Errorf("initialize k3s config: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}
	c.dynamicClient = dyn
	return c, nil
}

func loadKubeConfig(kubeconfigPath, apiServerURL string) (*rest.Config, error) {
	path := strings.TrimSpace(kubeconfigPath)
	if path == "" {
		path = strings.TrimSpace(os.Getenv("KUBECONFIG"))
	}

	var (
		cfg *rest.Config
		err error
	)
	if path != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig from %q: %w", path, err)
		}
	} else {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("load in-cluster config: %w", err)
		}
	}

	if host := strings.TrimSpace(apiServerURL); host != "" {
		cfg.Host = host
	}
	return cfg, nil
}

// EnsureNamespace creates or verifies existence of a team namespace.
// Returns namespace name on success.
func (c *Client) EnsureNamespace(ctx context.Context, teamID, teamSlug, planTier string) (string, error) {
	if c.config.DisableNamespaceIsolation {
		return fmt.Sprintf("team-%s", teamSlug), nil
	}
	if c.dynamicClient == nil {
		return "", fmt.Errorf("k3s dynamic client not initialized")
	}

	nsName := fmt.Sprintf("team-%s", teamSlug)
	namespace := map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": nsName,
			"labels": map[string]any{
				"app.0ops.io/managed-by":               "0ops",
				"app.0ops.io/team-id":                  teamID,
				"app.0ops.io/team-slug":                teamSlug,
				"app.0ops.io/plan":                     planTier,
				"kubernetes.io/metadata.name":          nsName,
				"pod-security.kubernetes.io/enforce":   "baseline",
				"pod-security.kubernetes.io/warn":      "restricted",
				"pod-security.kubernetes.io/audit":     "restricted",
			},
		},
	}
	if err := c.upsertResource(ctx, namespaceGVR, "", nsName, namespace); err != nil {
		return "", fmt.Errorf("ensure namespace %q: %w", nsName, err)
	}

	return nsName, nil
}

// EnsureTeamIsolation provisions a team namespace together with ResourceQuota,
// LimitRange and NetworkPolicies in a single atomic operation. If any step
// after namespace creation fails, the namespace is removed so the team is
// never left in a partially-isolated state (spec § 15 hard rule #3).
//
// ImagePullSecret bootstrap is not part of this orchestration because the
// GitHub App installation token wiring lands in M3.2; once available, call
// PatchGHCRImagePullSecret in the same saga step that consumes the install
// token (spec § 8.3).
func (c *Client) EnsureTeamIsolation(ctx context.Context, teamID, teamSlug, planTier string) (string, error) {
	if c.config.DisableNamespaceIsolation {
		return fmt.Sprintf("team-%s", teamSlug), nil
	}
	if c.dynamicClient == nil {
		return "", fmt.Errorf("k3s dynamic client not initialized")
	}

	nsName, err := c.EnsureNamespace(ctx, teamID, teamSlug, planTier)
	if err != nil {
		return "", err
	}

	rollback := func(step string, opErr error) error {
		if delErr := c.DeleteNamespace(ctx, nsName); delErr != nil {
			return fmt.Errorf("ensure team isolation %s: %w (rollback namespace %q failed: %v)", step, opErr, nsName, delErr)
		}
		return fmt.Errorf("ensure team isolation %s: %w", step, opErr)
	}

	if err := c.EnsureResourceQuota(ctx, nsName, planTier); err != nil {
		return "", rollback("resourcequota", err)
	}
	if err := c.EnsureLimitRange(ctx, nsName); err != nil {
		return "", rollback("limitrange", err)
	}
	if err := c.EnsureNetworkPolicy(ctx, nsName); err != nil {
		return "", rollback("networkpolicy", err)
	}
	return nsName, nil
}

// EnsureResourceQuota applies ResourceQuota to the namespace.
func (c *Client) EnsureResourceQuota(ctx context.Context, namespace, planTier string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}
	hard, ok := quotaByPlan(planTier)
	if !ok {
		return fmt.Errorf("unknown plan tier %q", planTier)
	}

	resourceQuota := map[string]any{
		"apiVersion": "v1",
		"kind":       "ResourceQuota",
		"metadata": map[string]any{
			"name":      "default",
			"namespace": namespace,
			"labels": map[string]any{
				"app.0ops.io/managed-by": "0ops",
				"app.0ops.io/plan":       planTier,
			},
		},
		"spec": map[string]any{
			"hard": hard,
		},
	}
	if err := c.upsertResource(ctx, resourceQuotaGVR, namespace, "default", resourceQuota); err != nil {
		return fmt.Errorf("ensure resourcequota in %q: %w", namespace, err)
	}

	return nil
}

// EnsureLimitRange applies LimitRange to the namespace.
func (c *Client) EnsureLimitRange(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}

	limitRange := map[string]any{
		"apiVersion": "v1",
		"kind":       "LimitRange",
		"metadata": map[string]any{
			"name":      "default",
			"namespace": namespace,
			"labels": map[string]any{
				"app.0ops.io/managed-by": "0ops",
			},
		},
		"spec": map[string]any{
			"limits": []any{
				map[string]any{
					"type": "Container",
					"defaultRequest": map[string]any{
						"cpu":    "100m",
						"memory": "256Mi",
					},
					"default": map[string]any{
						"cpu":    "500m",
						"memory": "1Gi",
					},
				},
			},
		},
	}
	if err := c.upsertResource(ctx, limitRangeGVR, namespace, "default", limitRange); err != nil {
		return fmt.Errorf("ensure limitrange in %q: %w", namespace, err)
	}

	return nil
}

// EnsureNetworkPolicy applies NetworkPolicy to the namespace.
func (c *Client) EnsureNetworkPolicy(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}

	ingressPolicy := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      "default-deny-ingress",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"}}},
						map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "cloudflare-tunnel"}}},
						map[string]any{"podSelector": map[string]any{}},
					},
				},
			},
		},
	}
	if err := c.upsertResource(ctx, networkPolicyGVR, namespace, "default-deny-ingress", ingressPolicy); err != nil {
		return fmt.Errorf("ensure ingress networkpolicy in %q: %w", namespace, err)
	}

	egressPolicy := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      "default-egress",
			"namespace": namespace,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{},
			"policyTypes": []any{"Egress"},
			"egress": []any{
				map[string]any{
					"to": []any{
						map[string]any{
							"ipBlock": map[string]any{
								"cidr": "0.0.0.0/0",
								"except": []any{
									"10.0.0.0/8",
									"172.16.0.0/12",
									"192.168.0.0/16",
								},
							},
						},
						map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"}}},
					},
				},
			},
		},
	}
	if err := c.upsertResource(ctx, networkPolicyGVR, namespace, "default-egress", egressPolicy); err != nil {
		return fmt.Errorf("ensure egress networkpolicy in %q: %w", namespace, err)
	}

	return nil
}

// PatchNamespacePSA patches PSA labels on a namespace.
func (c *Client) PatchNamespacePSA(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}

	resource := c.dynamicClient.Resource(namespaceGVR)
	obj, err := resource.Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get namespace %q: %w", namespace, err)
	}
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["pod-security.kubernetes.io/enforce"] = "baseline"
	labels["pod-security.kubernetes.io/warn"] = "restricted"
	labels["pod-security.kubernetes.io/audit"] = "restricted"
	obj.SetLabels(labels)
	if _, err := resource.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update namespace %q psa labels: %w", namespace, err)
	}

	return nil
}

// GetNamespace retrieves namespace details (for debugging/verification).
// M2 implementation: Returns nil.
func (c *Client) GetNamespace(ctx context.Context, namespace string) (map[string]interface{}, error) {
	if c.config.DisableNamespaceIsolation {
		return nil, nil
	}
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("k3s dynamic client not initialized")
	}

	obj, err := c.dynamicClient.Resource(namespaceGVR).Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get namespace %q: %w", namespace, err)
	}

	return obj.Object, nil
}

// DeleteNamespace removes a team namespace (used for team archival).
func (c *Client) DeleteNamespace(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}

	if err := c.dynamicClient.Resource(namespaceGVR).Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %q: %w", namespace, err)
	}

	return nil
}

// PatchGHCRImagePullSecret updates ghcr-pull secret in namespace.
// Used for refreshing GitHub App installation token.
func (c *Client) PatchGHCRImagePullSecret(ctx context.Context, namespace, dockerConfig string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(dockerConfig))
	secret := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      "ghcr-pull",
			"namespace": namespace,
			"labels": map[string]any{
				"app.0ops.io/managed-by": "0ops",
			},
		},
		"type": "kubernetes.io/dockerconfigjson",
		"data": map[string]any{
			".dockerconfigjson": encoded,
		},
	}
	if err := c.upsertResource(ctx, secretGVR, namespace, "ghcr-pull", secret); err != nil {
		return fmt.Errorf("ensure ghcr-pull secret in %q: %w", namespace, err)
	}

	return nil
}

func (c *Client) upsertResource(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	namespace, name string,
	desired map[string]any,
) error {
	var resource dynamic.ResourceInterface
	if namespace != "" {
		resource = c.dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resource = c.dynamicClient.Resource(gvr)
	}

	current, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, createErr := resource.Create(ctx, &unstructured.Unstructured{Object: desired}, metav1.CreateOptions{})
			return createErr
		}
		return err
	}

	obj := &unstructured.Unstructured{Object: desired}
	obj.SetResourceVersion(current.GetResourceVersion())
	_, err = resource.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

func quotaByPlan(plan string) (map[string]any, bool) {
	switch strings.TrimSpace(plan) {
	case "free":
		return map[string]any{
			"requests.cpu":          "1",
			"requests.memory":       "2Gi",
			"limits.cpu":            "2",
			"limits.memory":         "4Gi",
			"persistentvolumeclaims": "2",
			"pods":                  "5",
			"services":              "4",
		}, true
	case "starter":
		return map[string]any{
			"requests.cpu":          "4",
			"requests.memory":       "8Gi",
			"limits.cpu":            "8",
			"limits.memory":         "16Gi",
			"persistentvolumeclaims": "10",
			"pods":                  "30",
			"services":              "20",
		}, true
	case "pro":
		return map[string]any{
			"requests.cpu":          "16",
			"requests.memory":       "32Gi",
			"limits.cpu":            "32",
			"limits.memory":         "64Gi",
			"persistentvolumeclaims": "40",
			"pods":                  "120",
			"services":              "80",
		}, true
	case "team":
		return map[string]any{
			"requests.cpu":          "64",
			"requests.memory":       "128Gi",
			"limits.cpu":            "128",
			"limits.memory":         "256Gi",
			"persistentvolumeclaims": "100",
			"pods":                  "300",
			"services":              "200",
		}, true
	default:
		return nil, false
	}
}
