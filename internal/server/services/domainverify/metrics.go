package domainverify

import "time"

// Recorder closures default to no-op; bind via BindMetrics. Pattern mirrors
// internal/server/services/cloudflare/client.go.
var (
	recordVerifyAttemptFn   = func(string, string) {}
	recordExpiredCleanupFn  = func(string) {}
	recordGraceTransitionFn = func(string) {}
	recordPollerTickFn      = func(string, time.Duration) {}
)

// BindMetrics wires the four metric closures. Pass nil to reset to no-op.
//
//   - verifyAttempt(stage, outcome):
//     stage ∈ {"pending", "active"};
//     outcome ∈ {"success", "cname_missing", "txt_missing", "error"}.
//   - cleanupExpired(outcome): outcome ∈ {"expired", "noop"}.
//   - graceTransition(outcome): outcome ∈ {"marked", "cleared", "continued", "released"}.
//   - pollerTick(tick, latency): tick ∈ {"verifyPending", "checkUnhealthy", "cleanupExpired"}.
func BindMetrics(
	verifyAttempt func(stage, outcome string),
	cleanupExpired func(outcome string),
	graceTransition func(outcome string),
	pollerTick func(tick string, latency time.Duration),
) {
	if verifyAttempt == nil {
		recordVerifyAttemptFn = func(string, string) {}
	} else {
		recordVerifyAttemptFn = verifyAttempt
	}
	if cleanupExpired == nil {
		recordExpiredCleanupFn = func(string) {}
	} else {
		recordExpiredCleanupFn = cleanupExpired
	}
	if graceTransition == nil {
		recordGraceTransitionFn = func(string) {}
	} else {
		recordGraceTransitionFn = graceTransition
	}
	if pollerTick == nil {
		recordPollerTickFn = func(string, time.Duration) {}
	} else {
		recordPollerTickFn = pollerTick
	}
}

func recordVerifyAttempt(stage, outcome string) { recordVerifyAttemptFn(stage, outcome) }
func recordExpiredCleanup(outcome string)       { recordExpiredCleanupFn(outcome) }
func recordGraceTransition(outcome string)      { recordGraceTransitionFn(outcome) }
func recordPollerTick(tick string, latency time.Duration) {
	recordPollerTickFn(tick, latency)
}
