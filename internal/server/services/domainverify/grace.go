package domainverify

import "time"

// GraceWindow is the 7-day grace period before an unhealthy verified hostname
// is unbinded. Hard rule § 15 #8: 7 days is fixed.
const GraceWindow = 7 * 24 * time.Hour

// GraceAction enumerates the per-tick grace decision.
type GraceAction int

const (
	// GraceNoOp means the hostname is healthy and was not previously marked.
	GraceNoOp GraceAction = iota
	// GraceMarkUnhealthy means DNS just started failing; record now as failedAt.
	GraceMarkUnhealthy
	// GraceClearMark means DNS recovered while in unhealthy state.
	GraceClearMark
	// GraceContinue means DNS still failing but within 7-day grace window.
	GraceContinue
	// GraceRelease means grace window has elapsed; unbind hostname.
	GraceRelease
)

// GraceInput captures the per-tick inputs for a single binding.
type GraceInput struct {
	Now                 time.Time
	DNSPasses           bool
	HealthCheckFailedAt *time.Time
}

// GraceResult is the decision plus the suggested new failedAt timestamp
// (nil = clear).
type GraceResult struct {
	Action      GraceAction
	NewFailedAt *time.Time
}

// EvaluateGrace returns the grace action for one verified binding.
// spec § 8.2.
func EvaluateGrace(in GraceInput) GraceResult {
	if in.DNSPasses {
		if in.HealthCheckFailedAt != nil {
			return GraceResult{Action: GraceClearMark, NewFailedAt: nil}
		}
		return GraceResult{Action: GraceNoOp}
	}
	if in.HealthCheckFailedAt == nil {
		now := in.Now
		return GraceResult{Action: GraceMarkUnhealthy, NewFailedAt: &now}
	}
	if in.Now.Sub(*in.HealthCheckFailedAt) > GraceWindow {
		return GraceResult{Action: GraceRelease}
	}
	return GraceResult{Action: GraceContinue}
}
