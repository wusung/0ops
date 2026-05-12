// Package k3s provides K3s cluster client and namespace management utilities.
package k3s

import (
	"context"
	"fmt"
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
	config      *Config
	namespaces  map[string]map[string]interface{}
}

// NewClient creates a new K3s client from kubeconfig or in-cluster config.
// If DisableNamespaceIsolation is true, returns a no-op client.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	// TODO: Implement actual K3s client initialization in future iteration
	// - Add k8s.io/client-go and k8s.io/api dependencies
	// - Load kubeconfig from KubeconfigPath or env
	// - Or use in-cluster ServiceAccount if running inside K3s
	// - Initialize kubernetes.Clientset from rest.Config
	// - Test connectivity with a health check

	return &Client{
		config:     cfg,
		namespaces: map[string]map[string]interface{}{},
	}, nil
}

// EnsureNamespace creates or verifies existence of a team namespace.
// Returns namespace name on success.
// M2 implementation: Returns namespace name (actual K8s creation deferred to M3+).
func (c *Client) EnsureNamespace(_ context.Context, _, teamSlug, _ string) (string, error) {
	if teamSlug == "" {
		return "", fmt.Errorf("team slug is required")
	}
	if c.config.DisableNamespaceIsolation {
		return fmt.Sprintf("team-%s", teamSlug), nil
	}

	nsName := fmt.Sprintf("team-%s", teamSlug)
	state := c.ensureNamespaceState(nsName)
	state["namespace"] = map[string]interface{}{
		"name": nsName,
	}

	// TODO: Implement following steps in M3+:
	// 1. Call c.clientset.CoreV1().Namespaces().Get(ctx, nsName, metav1.GetOptions{})
	// 2. If exists, return nsName
	// 3. If not exists, create namespace with labels
	// 4. Create ResourceQuota for planTier
	// 5. Create LimitRange
	// 6. Create NetworkPolicy for ingress/egress
	// 7. Return nsName

	return nsName, nil
}

// EnsureResourceQuota applies ResourceQuota to the namespace.
// M2 implementation: No-op.
func (c *Client) EnsureResourceQuota(_ context.Context, namespace, planTier string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	quota, ok := resourceQuotaByPlan(planTier)
	if !ok {
		return fmt.Errorf("unknown plan tier: %s", planTier)
	}
	state := c.ensureNamespaceState(namespace)
	state["resource_quota"] = quota
	return nil
}

// EnsureLimitRange applies LimitRange to the namespace.
// M2 implementation: No-op.
func (c *Client) EnsureLimitRange(_ context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	state := c.ensureNamespaceState(namespace)
	state["limit_range"] = map[string]interface{}{
		"default_request_cpu":    "100m",
		"default_request_memory": "256Mi",
		"default_limit_cpu":      "500m",
		"default_limit_memory":   "1Gi",
	}
	return nil
}

// EnsureNetworkPolicy applies NetworkPolicy to the namespace.
// M2 implementation: No-op.
func (c *Client) EnsureNetworkPolicy(_ context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	state := c.ensureNamespaceState(namespace)
	state["network_policy"] = map[string]interface{}{
		"ingress_from": []string{"kube-system", "cloudflare-tunnel", "same-namespace"},
		"egress_mode":  "allow-internet-deny-rfc1918",
	}
	return nil
}

// PatchNamespacePSA patches PSA labels on a namespace.
// M2 implementation: No-op.
func (c *Client) PatchNamespacePSA(_ context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}
	state := c.ensureNamespaceState(namespace)
	state["psa"] = map[string]interface{}{
		"enforce": "baseline",
		"warn":    "restricted",
		"audit":   "restricted",
	}
	return nil
}

// GetNamespace retrieves namespace details (for debugging/verification).
// M2 implementation: Returns nil.
func (c *Client) GetNamespace(_ context.Context, namespace string) (map[string]interface{}, error) {
	if c.config.DisableNamespaceIsolation {
		return nil, nil
	}
	state, ok := c.namespaces[namespace]
	if !ok {
		return nil, nil
	}
	return state, nil
}

// DeleteNamespace removes a team namespace (used for team archival).
// M2 implementation: No-op.
func (c *Client) DeleteNamespace(_ context.Context, _ string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Call c.clientset.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	// 2. Handle grace period / force deletion

	return nil
}

// PatchGHCRImagePullSecret updates ghcr-pull secret in namespace.
// Used for refreshing GitHub App installation token.
// M2 implementation: No-op.
func (c *Client) PatchGHCRImagePullSecret(_ context.Context, _, _ string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Build Secret spec with dockercfg: dockerConfig
	// 2. Call c.clientset.CoreV1().Secrets(namespace).Create/Update(ctx, secret, metav1.CreateOptions{})

	return nil
}

func (c *Client) ensureNamespaceState(namespace string) map[string]interface{} {
	if state, ok := c.namespaces[namespace]; ok {
		return state
	}
	state := map[string]interface{}{}
	c.namespaces[namespace] = state
	return state
}

func resourceQuotaByPlan(planTier string) (map[string]string, bool) {
	switch planTier {
	case "free":
		return map[string]string{
			"requests.cpu": "1", "requests.memory": "2Gi",
			"limits.cpu": "2", "limits.memory": "4Gi",
			"pods": "5", "persistentvolumeclaims": "2", "services": "4",
		}, true
	case "starter":
		return map[string]string{
			"requests.cpu": "4", "requests.memory": "8Gi",
			"limits.cpu": "8", "limits.memory": "16Gi",
			"pods": "30", "persistentvolumeclaims": "10", "services": "20",
		}, true
	case "pro":
		return map[string]string{
			"requests.cpu": "16", "requests.memory": "32Gi",
			"limits.cpu": "32", "limits.memory": "64Gi",
			"pods": "120", "persistentvolumeclaims": "40", "services": "80",
		}, true
	case "team":
		return map[string]string{
			"requests.cpu": "64", "requests.memory": "128Gi",
			"limits.cpu": "128", "limits.memory": "256Gi",
			"pods": "300", "persistentvolumeclaims": "100", "services": "200",
		}, true
	default:
		return nil, false
	}
}
