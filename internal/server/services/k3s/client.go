// Package k3s provides K3s cluster client and namespace management utilities.
package k3s

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	tcp      = apiv1.ProtocolTCP
	udp      = apiv1.ProtocolUDP
	allPorts = intstr.FromInt(0)
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
	clientset     kubernetes.Interface
}

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
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}
	c.dynamicClient = dyn
	c.clientset = clientset
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

	nsName := fmt.Sprintf("team-%s", teamSlug)

	ns := &apiv1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
			Labels: map[string]string{
				"app.0ops.io/managed-by": "0ops",
				"app.0ops.io/team-slug":  teamSlug,
				"app.0ops.io/team-id":    teamID,
				"app.0ops.io/plan":       planTier,
				"pod-security.kubernetes.io/enforce": "baseline",
				"pod-security.kubernetes.io/warn":    "restricted",
				"pod-security.kubernetes.io/audit":   "restricted",
			},
		},
	}

	_, err := c.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		// Check if namespace already exists
		if strings.Contains(err.Error(), "already exists") {
			return nsName, nil
		}
		return "", fmt.Errorf("create namespace: %w", err)
	}

	// Create ResourceQuota, LimitRange, NetworkPolicy in same transaction
	if err := c.EnsureResourceQuota(ctx, nsName, planTier); err != nil {
		return "", fmt.Errorf("ensure resource quota: %w", err)
	}
	if err := c.EnsureLimitRange(ctx, nsName); err != nil {
		return "", fmt.Errorf("ensure limit range: %w", err)
	}
	if err := c.EnsureNetworkPolicy(ctx, nsName); err != nil {
		return "", fmt.Errorf("ensure network policy: %w", err)
	}

	return nsName, nil
}

// tierResources returns ResourceQuota values for a given plan tier.
func tierResources(planTier string) (cpu, mem, cpuLimit, memLimit string, err error) {
	switch planTier {
	case "free":
		return "1", "2Gi", "2", "4Gi", nil
	case "starter":
		return "4", "8Gi", "8", "16Gi", nil
	case "pro":
		return "16", "32Gi", "32", "64Gi", nil
	case "team":
		return "64", "128Gi", "128", "256Gi", nil
	default:
		return "", "", "", "", fmt.Errorf("unknown plan tier: %s", planTier)
	}
}

// EnsureResourceQuota applies ResourceQuota to the namespace.
func (c *Client) EnsureResourceQuota(ctx context.Context, namespace, planTier string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	cpu, mem, cpuLimit, memLimit, err := tierResources(planTier)
	if err != nil {
		return err
	}

	rq := &apiv1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: namespace,
			Labels: map[string]string{
				"app.0ops.io/plan":       planTier,
				"app.0ops.io/managed-by": "0ops",
			},
		},
		Spec: apiv1.ResourceQuotaSpec{
			Hard: apiv1.ResourceList{
				"requests.cpu":                resource.MustParse(cpu),
				"requests.memory":             resource.MustParse(mem),
				"limits.cpu":                  resource.MustParse(cpuLimit),
				"limits.memory":               resource.MustParse(memLimit),
				"persistentvolumeclaims":      resource.MustParse("2"),
				"pods":                        resource.MustParse("5"),
				"services":                    resource.MustParse("4"),
			},
		},
	}

	_, err = c.clientset.CoreV1().ResourceQuotas(namespace).Create(ctx, rq, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create resource quota: %w", err)
	}

	return nil
}

// EnsureLimitRange applies LimitRange to the namespace.
func (c *Client) EnsureLimitRange(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	lr := &apiv1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: namespace,
			Labels: map[string]string{
				"app.0ops.io/managed-by": "0ops",
			},
		},
		Spec: apiv1.LimitRangeSpec{
			Limits: []apiv1.LimitRangeItem{
				{
					Type: apiv1.LimitTypeContainer,
					Default: apiv1.ResourceList{
						apiv1.ResourceCPU:    resource.MustParse("100m"),
						apiv1.ResourceMemory: resource.MustParse("128Mi"),
					},
					DefaultRequest: apiv1.ResourceList{
						apiv1.ResourceCPU:    resource.MustParse("10m"),
						apiv1.ResourceMemory: resource.MustParse("32Mi"),
					},
				},
				{
					Type: apiv1.LimitTypePod,
					Max: apiv1.ResourceList{
						apiv1.ResourceCPU:    resource.MustParse("4"),
						apiv1.ResourceMemory: resource.MustParse("4Gi"),
					},
					Min: apiv1.ResourceList{
						apiv1.ResourceCPU:    resource.MustParse("10m"),
						apiv1.ResourceMemory: resource.MustParse("32Mi"),
					},
				},
			},
		},
	}

	_, err := c.clientset.CoreV1().LimitRanges(namespace).Create(ctx, lr, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create limit range: %w", err)
	}

	return nil
}

// EnsureNetworkPolicy applies NetworkPolicy to the namespace.
func (c *Client) EnsureNetworkPolicy(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// Ingress policy: Allow from traefik and same namespace
	ingressPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-ingress",
			Namespace: namespace,
			Labels: map[string]string{
				"app.0ops.io/managed-by": "0ops",
			},
		},
		Spec: netv1.NetworkPolicySpec{
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
			PodSelector: metav1.LabelSelector{},
			Ingress: []netv1.NetworkPolicyIngressRule{
				{
					From: []netv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{},
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": namespace,
								},
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": "kube-system",
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app.kubernetes.io/name": "traefik",
								},
							},
						},
					},
				},
			},
		},
	}

	// Egress policy: Allow to external but deny RFC1918
	egressPolicy := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allow-egress",
			Namespace: namespace,
			Labels: map[string]string{
				"app.0ops.io/managed-by": "0ops",
			},
		},
		Spec: netv1.NetworkPolicySpec{
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			PodSelector: metav1.LabelSelector{},
			Egress: []netv1.NetworkPolicyEgressRule{
				{
					To: []netv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"k8s-app": "kube-dns",
								},
							},
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": "kube-system",
								},
							},
						},
						{
							IPBlock: &netv1.IPBlock{
								CIDR: "0.0.0.0/0",
								Except: []string{
									"10.0.0.0/8",
									"172.16.0.0/12",
									"192.168.0.0/16",
								},
							},
						},
					},
					Ports: []netv1.NetworkPolicyPort{
						{
							Protocol: &tcp,
							Port:     &allPorts,
						},
						{
							Protocol: &udp,
							Port:     &allPorts,
						},
					},
				},
			},
		},
	}

	// Create ingress policy
	_, err := c.clientset.NetworkingV1().NetworkPolicies(namespace).Create(ctx, ingressPolicy, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create ingress network policy: %w", err)
	}

	// Create egress policy
	_, err = c.clientset.NetworkingV1().NetworkPolicies(namespace).Create(ctx, egressPolicy, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("create egress network policy: %w", err)
	}

	return nil
}

// PatchNamespacePSA patches PSA labels on a namespace.
func (c *Client) PatchNamespacePSA(ctx context.Context, namespace string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	patch := []interface{}{
		map[string]interface{}{
			"op":    "replace",
			"path":  "/metadata/labels/pod-security.kubernetes.io~1enforce",
			"value": "baseline",
		},
		map[string]interface{}{
			"op":    "replace",
			"path":  "/metadata/labels/pod-security.kubernetes.io~1warn",
			"value": "restricted",
		},
		map[string]interface{}{
			"op":    "replace",
			"path":  "/metadata/labels/pod-security.kubernetes.io~1audit",
			"value": "restricted",
		},
	}

	data, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal JSON patch: %w", err)
	}

	_, err = c.clientset.CoreV1().Namespaces().Patch(ctx, namespace, types.JSONPatchType, data, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patch namespace PSA labels: %w", err)
	}

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
// token format: base64(<GitHub App installation token>)
func (c *Client) PatchGHCRImagePullSecret(ctx context.Context, namespace, token string) error {
	if c.config.DisableNamespaceIsolation {
		return nil
	}

	// Build dockerconfigjson
	dockerAuth := map[string]interface{}{
		"auths": map[string]interface{}{
			"ghcr.io": map[string]string{
				"username": "x-access-token",
				"password": token,
				"auth":     token,
			},
		},
	}

	dockerConfigJSON, err := json.Marshal(dockerAuth)
	if err != nil {
		return fmt.Errorf("marshal docker auth: %w", err)
	}

	secret := &apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghcr-pull",
			Namespace: namespace,
			Labels: map[string]string{
				"app.0ops.io/managed-by": "0ops",
			},
		},
		Type: apiv1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			apiv1.DockerConfigJsonKey: dockerConfigJSON,
		},
	}

	_, err = c.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// If secret exists, update it
		if strings.Contains(err.Error(), "already exists") {
			_, err = c.clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("update ghcr-pull secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("create ghcr-pull secret: %w", err)
	}

	return nil
}
