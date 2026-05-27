package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"

	"github.com/winshare/zeroops/internal/server/leader"
	"github.com/winshare/zeroops/internal/server/observability"
)

// buildLeader resolves an OPS_LEADER_MODE value into a Leader plus an
// optional Run handle (lease mode only). The kubeconfigPath parameter
// is the OPS_LEADER_KUBECONFIG / KUBECONFIG fallback; empty falls back
// to in-cluster config. Spec § 4.3 + § 14 hard rule #1/#5.
func buildLeader(mode, identity string, metrics *observability.Metrics, kubeconfigPath string) (leader.Leader, func(ctx context.Context), error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "always"
	}
	switch mode {
	case "always":
		return leader.AlwaysLeader{Name: identity}, nil, nil
	case "lease":
		restCfg, err := loadLeaderKubeConfig(kubeconfigPath)
		if err != nil {
			return nil, nil, fmt.Errorf("OPS_LEADER_MODE=lease: %w", err)
		}
		cs, err := kubernetes.NewForConfig(restCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("OPS_LEADER_MODE=lease: build clientset: %w", err)
		}
		obs := newMetricsLeaderObserver(metrics)
		// client-go's MetricsProvider is registered once globally (its
		// SetProvider gate is sync.Once); we wire it here so renew /
		// slowpath events flow through the same Observer.
		leaderelection.SetProvider(leader.PrometheusProvider{Observer: obs})
		ll, err := leader.NewLeaseLeader(leader.Config{
			Namespace: envOr("OPS_LEADER_NAMESPACE", "system-0ops"),
			LeaseName: envOr("OPS_LEADER_LEASE_NAME", "0ops-backend-leader"),
			Identity:  identity,
			Client:    cs.CoordinationV1(),
			Observer:  obs,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("OPS_LEADER_MODE=lease: %w", err)
		}
		run := func(ctx context.Context) { ll.Run(ctx) }
		return ll, run, nil
	default:
		return nil, nil, fmt.Errorf("OPS_LEADER_MODE must be 'always' or 'lease', got %q", mode)
	}
}

func loadLeaderKubeConfig(path string) (*rest.Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(os.Getenv("KUBECONFIG"))
	}
	if path != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig %q: %w", path, err)
		}
		return cfg, nil
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster config: %w", err)
	}
	return cfg, nil
}

// reconcilerLeaderGate adapts leader.Leader (IsLeader + Identity) onto
// reconciler.Leader (IsLeader only). The reconciler intentionally
// holds a minimal interface to avoid an import cycle.
type reconcilerLeaderGate struct{ l leader.Leader }

// IsLeader reports whether the local pod is the leader.
func (g reconcilerLeaderGate) IsLeader() bool { return g.l.IsLeader() }

// metricsLeaderObserver fans leader lifecycle callbacks onto the
// observability.Metrics surface. Spec § 8.1 — every callback feeds a
// pod_name-labelled metric so the leader handover / lease renew
// dashboards stay in sync with the active Lease state.
type metricsLeaderObserver struct {
	metrics *observability.Metrics
}

func newMetricsLeaderObserver(m *observability.Metrics) *metricsLeaderObserver {
	return &metricsLeaderObserver{metrics: m}
}

// OnGained flips the leader_status gauge to 1.
func (o *metricsLeaderObserver) OnGained(id string) {
	o.metrics.SetLeaderStatus(id, true)
}

// OnLost flips the leader_status gauge to 0.
func (o *metricsLeaderObserver) OnLost(id string) {
	o.metrics.SetLeaderStatus(id, false)
}

// OnNewLeader emits the handover counter for the current pod.
func (o *metricsLeaderObserver) OnNewLeader(currentID, _ string) {
	o.metrics.ObserveLeaderHandover(currentID)
}

// OnLeaseRenew emits the lease_renew counter with outcome label. The
// pod label resolves to "" → "unknown" on the metric side because
// client-go's MetricsProvider does not pass pod identity through; the
// process-global provider does not know per-call identity. The HA
// dashboard joins on the leader_status series for pod-level views.
func (o *metricsLeaderObserver) OnLeaseRenew(outcome string) {
	o.metrics.ObserveLeaseRenew("", outcome)
}
