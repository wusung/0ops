package leader

// Leader gates leader-only work (reconciler loops, preview cleanup,
// domain verify polling, token refresh) so exactly one backend pod
// drives the side-effecting work at any time. Callers use IsLeader() in
// a pull model — every loop tick checks the gate. Identity is the
// stable holder identity reported to the underlying lease, used for
// pod_name metric labels and log lines.
//
// The interface deliberately omits a push-channel; reconciler.Runner +
// observability.Metrics already cover the only consumers, and a
// channel would be dead weight until a real subscriber materialises
// (see spec § 4.1 and the M5.5 todo "Out of scope" note).
type Leader interface {
	IsLeader() bool
	Identity() string
}

// AlwaysLeader is the v1 / dev stub (spec § 4.3, OPS_LEADER_MODE=always):
// backend runs as a single replica, so every gate check wins. Production
// (OPS_LEADER_MODE=lease) swaps in LeaseLeader.
type AlwaysLeader struct {
	// Name is the identity reported by Identity(); falls back to
	// PodIdentity() when empty so dev compose still gets a stable
	// pod_name label.
	Name string
}

// IsLeader reports whether the gate currently allows leader-only work.
// AlwaysLeader returns true unconditionally; LeaseLeader tracks the
// real Lease state.
func (a AlwaysLeader) IsLeader() bool { return true }

// Identity returns the holder identity for AlwaysLeader. Empty Name is
// replaced by PodIdentity() so callers always see a non-empty label.
func (a AlwaysLeader) Identity() string {
	if a.Name == "" {
		return PodIdentity()
	}
	return a.Name
}
