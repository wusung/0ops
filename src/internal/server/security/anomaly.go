package security

import "github.com/winshare/zeroops/internal/server/services/audit"

// AbuseDetectedAction is the audit action emitted when a token anomaly
// reaction fires (spec § 6.2). It reuses the existing abuse_detected
// channel — this package does NOT define a new event class or a new
// detection loop (spec hard rule #5).
const AbuseDetectedAction = "abuse_detected"

// AnomalySubjectType is the audit subject_type for a token anomaly row.
const AnomalySubjectType = "token"

// AnomalySignals are the per-token signals available in v1 (spec § 6.1).
// Geo/IP jump is deliberately absent: it depends on access_log_aggregate,
// which does not exist in v1 (rate-limit-and-abuse, deferred). Its absence
// here is the honest expression of spec § 6.1 / hard rule #4 — no
// access_log_aggregate dependency path is reachable from v1 code.
type AnomalySignals struct {
	// SustainedRateLimit is true when a token persistently tops its rate
	// bucket (not a single 429), derived from the existing limiter metric.
	SustainedRateLimit bool
	// ForbiddenWriteRatio is the share of a token's recent requests that hit
	// 403 on write scopes — a coarse v1 proxy for scope abuse.
	ForbiddenWriteRatio float64
}

// AnomalyThresholds parameterise EvaluateAnomaly. v1 uses fixed thresholds
// (no per-token historical baseline; that is spec § 12 open).
type AnomalyThresholds struct {
	ForbiddenWriteRatio float64
}

// DefaultAnomalyThresholds returns the v1 fixed thresholds.
func DefaultAnomalyThresholds() AnomalyThresholds {
	return AnomalyThresholds{ForbiddenWriteRatio: 0.5}
}

// AnomalyDecision is the pure evaluation result. The rate-limit-and-abuse
// background detector (deferred) is the only intended caller; this package
// supplies the signal semantics and the reaction policy, not a goroutine.
type AnomalyDecision struct {
	// Detected is true when a reaction should fire.
	Detected bool
	// AuditAction / SubjectType describe the audit row to write when Detected.
	AuditAction string
	SubjectType string
	// Reason is a short human-readable cause for the audit args.
	Reason string
	// AutoThrottle / RequireReauth are reaction toggles. v1 keeps both false:
	// alert-only, no automatic blocking (spec § 6.2, hard rule #5).
	AutoThrottle  bool
	RequireReauth bool
}

// EvaluateAnomaly applies the v1 fixed-threshold policy. Pure function.
func EvaluateAnomaly(sig AnomalySignals, thresholds AnomalyThresholds) AnomalyDecision {
	reason := ""
	switch {
	case sig.SustainedRateLimit:
		reason = "sustained_rate_limit"
	case sig.ForbiddenWriteRatio >= thresholds.ForbiddenWriteRatio:
		reason = "forbidden_write_ratio"
	default:
		return AnomalyDecision{}
	}
	return AnomalyDecision{
		Detected:    true,
		AuditAction: AbuseDetectedAction,
		SubjectType: AnomalySubjectType,
		Reason:      reason,
		// v1 alert-only; both reactions remain off until rate-limit-and-abuse
		// lands the limiter integration and auth-and-rbac the reauth flag.
		AutoThrottle:  false,
		RequireReauth: false,
	}
}

// AnomalyAuditEntry builds the audit.Entry a detected anomaly should write.
// spec § 6.2 names the actor "system:anomaly"; audit_log.actor_user_id is a
// user FK, so this is realised as Source=system with a nil user actor — no
// fabricated UUID (spec hard rule #4). The caller (rate-limit-and-abuse,
// deferred) routes this through the existing audit.Log path.
func AnomalyAuditEntry(teamID, tokenID string, dec AnomalyDecision) audit.Entry {
	subjectID := tokenID
	return audit.Entry{
		TeamID:      teamID,
		ActorUserID: nil,
		Source:      audit.SourceSystem,
		SubjectType: dec.SubjectType,
		SubjectID:   &subjectID,
		Action:      dec.AuditAction,
		Args:        map[string]any{"reason": dec.Reason},
		Result:      map[string]any{"auto_throttle": dec.AutoThrottle, "require_reauth": dec.RequireReauth},
		Outcome:     audit.OutcomeSuccess,
	}
}
