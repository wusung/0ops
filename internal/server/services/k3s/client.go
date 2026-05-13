// Package k3s provides K3s cluster client and namespace management utilities.
package k3s

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
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

	return &Client{config: cfg}, nil
}

// EnsureNamespace creates or verifies existence of a team namespace.
// Returns namespace name on success.
// M2 implementation: Returns namespace name (actual K8s creation deferred to M3+).
func (c *Client) EnsureNamespace(_ context.Context, _, teamSlug, _ string) (string, error) {
	if c.config.DisableNamespaceIsolation {
		return fmt.Sprintf("team-%s", teamSlug), nil
	}

	nsName := fmt.Sprintf("team-%s", teamSlug)

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
func (c *Client) EnsureResourceQuota(_ context.Context, _, _ string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Build ResourceQuota spec based on planTier
	// 2. Call c.clientset.CoreV1().ResourceQuotas(namespace).Create/Update(ctx, rq, metav1.CreateOptions{})
	// 3. Handle quota exceeded errors gracefully

	return nil
}

// EnsureLimitRange applies LimitRange to the namespace.
// M2 implementation: No-op.
func (c *Client) EnsureLimitRange(_ context.Context, _ string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Build LimitRange spec
	// 2. Call c.clientset.CoreV1().LimitRanges(namespace).Create/Update(ctx, lr, metav1.CreateOptions{})

	return nil
}

// EnsureNetworkPolicy applies NetworkPolicy to the namespace.
// M2 implementation: No-op.
func (c *Client) EnsureNetworkPolicy(_ context.Context, _ string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Build NetworkPolicy specs for ingress and egress
	// 2. Call c.clientset.NetworkingV1().NetworkPolicies(namespace).Create/Update(ctx, np, metav1.CreateOptions{})

	return nil
}

// PatchNamespacePSA patches PSA labels on a namespace.
// M2 implementation: No-op.
func (c *Client) PatchNamespacePSA(_ context.Context, _ string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Apply labels:
	//    - pod-security.kubernetes.io/enforce: baseline
	//    - pod-security.kubernetes.io/warn: restricted
	// 2. Use json.Patch to update namespace

	return nil
}

// GetNamespace retrieves namespace details (for debugging/verification).
// M2 implementation: Returns nil.
func (c *Client) GetNamespace(_ context.Context, _ string) (map[string]interface{}, error) {
	if c.config.DisableNamespaceIsolation {
		return nil, nil
	}

	// TODO: Implement in M3+:
	// 1. Call c.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	// 2. Return namespace details for verification

	return nil, nil
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
