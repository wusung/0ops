package reconciler

// Leader gates background loops so only one backend pod drives the
// reconciliation work at any time. M5.5 (backend-ha-leader-election)
// will swap the AlwaysLeader stub for a Kubernetes-Lease-backed
// implementation; the reconciler depends on the interface, never the
// concrete type, so the swap is mechanical.
type Leader interface {
	IsLeader() bool
}

// AlwaysLeader is the v1 / M5.3 stub: backend runs as a single replica,
// so every tick wins the leader gate. The HA replicas in M5.5 will
// replace this implementation without touching reconciler call sites.
type AlwaysLeader struct{}

// IsLeader reports whether the current pod is the active leader. The
// stub returns true so reconciler loops run unconditionally in v1.
func (AlwaysLeader) IsLeader() bool { return true }
