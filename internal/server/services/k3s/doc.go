// Package k3s provides K3s cluster client and team namespace management.
//
// # K3s Client
//
// The Client type wraps kubernetes.Interface and provides high-level operations for:
//   - Creating/verifying team namespaces (per spec § 4.2: team-<slug>)
//   - Applying ResourceQuota based on plan tier (per spec § 5.1)
//   - Applying LimitRange with pod/container limits
//   - Configuring NetworkPolicy for ingress/egress
//   - Patching Pod Security Admission (PSA) labels
//   - Managing ghcr-pull ImagePullSecret for GHCR authentication
//
// # Namespace Layout
//
// Two namespace patterns are managed:
//   - system-0ops: Backend self, no ResourceQuota (to avoid self-starving)
//   - team-<slug>: Managed apps, with ResourceQuota/LimitRange/NetworkPolicy/PSA per plan tier
//
// # Configuration
//
// NewClient accepts a Config struct:
//
//	cfg := &k3s.Config{
//	    KubeconfigPath: "/home/user/.kube/config",
//	    DisableNamespaceIsolation: true, // for dev without cluster
//	}
//	client, err := k3s.NewClient(cfg)
//
// DisableNamespaceIsolation=true enables testing and development without a live K3s cluster.
// All operations become no-ops and succeed silently.
//
// # ResourceQuota Tiers
//
// ResourceQuota values (per spec § 5.1):
//   - free:    1 CPU, 2Gi mem,    5 pods, 2 PVC
//   - starter: 4 CPU, 8Gi mem,   30 pods, 10 PVC
//   - pro:     16 CPU, 32Gi mem, 120 pods, 40 PVC
//   - team:    64 CPU, 128Gi mem, 300 pods, 100 PVC
//
// # Integration Points
//
// K3s client is used by:
//   - create_app handler (EnsureNamespace called during app creation)
//   - team creation (system-0ops bootstrap)
//   - team archival/deletion (DeleteNamespace)
//   - reconciler (periodic ImagePullSecret refresh via PatchGHCRImagePullSecret)
//
// # TODO: Implementation Status
//
// Current implementation is a skeleton. Full implementation requires:
//   - K8s client-go v1.x+ dependency (add to go.mod)
//   - Kubeconfig loading logic
//   - ResourceQuota/LimitRange/NetworkPolicy/PSA manifest building
//   - Error handling and retries for K8s API calls
//   - Unit tests with k8s.io/client-go/fake clientset
//   - Integration tests against real K3s cluster
//   - Documentation on K3s cluster setup (kubeconfig, RBAC, api-server access)
package k3s
