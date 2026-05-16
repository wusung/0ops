package leader

import (
	"k8s.io/client-go/tools/leaderelection"
)

// Observer receives lifecycle notifications from a Leader. Production
// wires this onto observability.Metrics so the zeroops_leader_*
// counters/gauges (spec § 8.1) stay in sync with the Lease state.
//
// Callers must treat callbacks as best-effort: a slow or panicking
// Observer must not block leader election. Implementations should
// emit metrics + a structured log line and return.
type Observer interface {
	// OnGained fires when the local pod transitions to leader.
	OnGained(id string)

	// OnLost fires when the local pod loses leadership (renew failure,
	// SIGTERM, ctx cancel). It is also called once on Run() exit even
	// if OnGained never fired — see client-go leaderelection docs.
	OnLost(id string)

	// OnNewLeader fires when the cluster observes a leader transition.
	// currentID is the local pod's identity; newID is the holder
	// identity reported by the latest Lease record. Implementations
	// should only count this as a handover when newID != currentID
	// AND the previously reported newID differs (avoid double-count
	// during initial acquire).
	OnNewLeader(currentID, newID string)

	// OnLeaseRenew fires on every successful renew / lost-renew /
	// slow acquire event surfaced by client-go's MetricsProvider. The
	// outcome label is one of: "acquired", "lost", "slow_acquire".
	OnLeaseRenew(outcome string)
}

// NopObserver discards every callback. Useful for tests and for the
// AlwaysLeader path where no metrics fire.
type NopObserver struct{}

// OnGained is a no-op.
func (NopObserver) OnGained(string) {}

// OnLost is a no-op.
func (NopObserver) OnLost(string) {}

// OnNewLeader is a no-op.
func (NopObserver) OnNewLeader(string, string) {}

// OnLeaseRenew is a no-op.
func (NopObserver) OnLeaseRenew(string) {}

// PrometheusProvider adapts client-go's leaderelection.MetricsProvider
// onto an Observer so renew / lost / slowpath events flow to the same
// pod_name-labelled counters as OnGained/OnLost/OnNewLeader. Register
// once at process start via leaderelection.SetProvider — client-go's
// onlyOnce gate makes repeated calls a no-op.
type PrometheusProvider struct {
	Observer Observer
}

// NewLeaderMetric returns a per-lease metric bridge.
func (p PrometheusProvider) NewLeaderMetric() leaderelection.LeaderMetric {
	return leaderMetricBridge{observer: p.Observer}
}

type leaderMetricBridge struct {
	observer Observer
}

// On is called by client-go when the local pod begins leading.
func (b leaderMetricBridge) On(string) {
	if b.observer == nil {
		return
	}
	b.observer.OnLeaseRenew("acquired")
}

// Off is called by client-go when the local pod stops leading.
func (b leaderMetricBridge) Off(string) {
	if b.observer == nil {
		return
	}
	b.observer.OnLeaseRenew("lost")
}

// SlowpathExercised is called by client-go when acquire has to wait
// the full lease duration (cluster jitter / contention).
func (b leaderMetricBridge) SlowpathExercised(string) {
	if b.observer == nil {
		return
	}
	b.observer.OnLeaseRenew("slow_acquire")
}
