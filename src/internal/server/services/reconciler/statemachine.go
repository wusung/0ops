package reconciler

import (
	"fmt"
)

// DeployStatus enumerates the deploy_run statuses participating in the
// reconciler-and-incident state machine (spec § 6.1). Values mirror the
// strings persisted in deploy_run.status.
type DeployStatus string

const (
	StatusQueued             DeployStatus = "queued"
	StatusPreparing          DeployStatus = "preparing"
	StatusBuilding           DeployStatus = "building"
	StatusPushing            DeployStatus = "pushing"
	StatusRendering          DeployStatus = "rendering"
	StatusSyncing            DeployStatus = "syncing"
	StatusLive               DeployStatus = "live"
	StatusFailed             DeployStatus = "failed"
	StatusCompensating       DeployStatus = "compensating"
	StatusRolledBack         DeployStatus = "rolled_back"
	StatusFailedPermanently  DeployStatus = "failed_permanently"
	StatusCanceled           DeployStatus = "canceled"
)

// AllFinalStatuses lists every terminal status. Once a deploy_run hits
// one of these, it is immutable (spec § 16 #9).
var AllFinalStatuses = []DeployStatus{
	StatusLive,
	StatusFailed,
	StatusRolledBack,
	StatusFailedPermanently,
	StatusCanceled,
}

// IsTerminal reports whether the status ends the deploy_run lifecycle.
func IsTerminal(s DeployStatus) bool {
	switch s {
	case StatusLive, StatusFailed, StatusRolledBack, StatusFailedPermanently, StatusCanceled:
		return true
	default:
		return false
	}
}

// RequiresFailureClassification reports whether the target status MUST
// carry a non-nil failure_classification (spec § 6.2 + § 16 #1).
// canceled is excluded — operators cancel intentionally and the row
// has no classifiable fault.
func RequiresFailureClassification(s DeployStatus) bool {
	switch s {
	case StatusFailed, StatusRolledBack, StatusFailedPermanently:
		return true
	default:
		return false
	}
}

// allowedTransitions encodes the spec § 6.1 forward + failure edges
// the reconciler / callback / saga code may apply. The state machine
// rejects any transition not listed here; callers that need a new edge
// must update both this map and the spec.
var allowedTransitions = map[DeployStatus]map[DeployStatus]struct{}{
	StatusQueued: {
		StatusPreparing: {},
		StatusFailed:    {},
		StatusCanceled:  {},
	},
	StatusPreparing: {
		StatusBuilding:    {},
		StatusFailed:      {},
		StatusCompensating: {},
		StatusCanceled:    {},
	},
	StatusBuilding: {
		StatusPushing:  {},
		StatusFailed:   {},
		StatusCanceled: {},
	},
	StatusPushing: {
		StatusRendering: {},
		StatusFailed:    {},
		StatusCanceled:  {},
	},
	StatusRendering: {
		StatusSyncing: {},
		StatusFailed:  {},
		StatusCanceled: {},
	},
	StatusSyncing: {
		StatusLive:   {},
		StatusFailed: {},
		StatusCanceled: {},
	},
	StatusCompensating: {
		StatusRolledBack:         {},
		StatusFailedPermanently:  {},
	},
}

// ErrIllegalTransition surfaces a contract violation that callers must
// treat as a programming bug (spec § 6.4: fail-fast). The reconciler
// runner converts it into a panic; tests assert the value directly.
type ErrIllegalTransition struct {
	From DeployStatus
	To   DeployStatus
}

func (e ErrIllegalTransition) Error() string {
	return fmt.Sprintf("reconciler: illegal transition %s → %s", e.From, e.To)
}

// ErrMissingClassification surfaces a fail-final transition that did
// not carry the mandatory failure_classification payload (spec § 16 #1).
type ErrMissingClassification struct {
	To DeployStatus
}

func (e ErrMissingClassification) Error() string {
	return fmt.Sprintf("reconciler: transition to %s requires failure_classification", e.To)
}

// TransitionPayload is the input to Lint() / Transition(). The
// classification pointer mirrors how the DB column is nullable in the
// non-failure branches.
type TransitionPayload struct {
	From                  DeployStatus
	To                    DeployStatus
	FailureClassification *string
	ErrorSummary          *string
}

// Lint verifies the From → To pair is allowed and the payload satisfies
// the failure_classification invariant. Returns nil on success;
// ErrIllegalTransition / ErrMissingClassification on contract failures.
// The runner uses Lint to gate every Transition() call and panics on
// non-nil — illegal transitions are programmer error, never recoverable
// runtime state.
func Lint(payload TransitionPayload) error {
	if payload.From == payload.To {
		return ErrIllegalTransition{From: payload.From, To: payload.To}
	}
	allowed, ok := allowedTransitions[payload.From]
	if !ok {
		return ErrIllegalTransition{From: payload.From, To: payload.To}
	}
	if _, ok := allowed[payload.To]; !ok {
		return ErrIllegalTransition{From: payload.From, To: payload.To}
	}
	if RequiresFailureClassification(payload.To) {
		if payload.FailureClassification == nil || *payload.FailureClassification == "" {
			return ErrMissingClassification{To: payload.To}
		}
	}
	return nil
}
