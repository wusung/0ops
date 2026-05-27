package reconciler

import (
	"errors"
	"testing"
)

func TestLintAllowsHappyPathTransitions(t *testing.T) {
	cases := []struct {
		from DeployStatus
		to   DeployStatus
	}{
		{StatusQueued, StatusPreparing},
		{StatusPreparing, StatusBuilding},
		{StatusBuilding, StatusPushing},
		{StatusPushing, StatusRendering},
		{StatusRendering, StatusSyncing},
		{StatusSyncing, StatusLive},
		{StatusPreparing, StatusCompensating},
		{StatusCompensating, StatusRolledBack},
		{StatusCompensating, StatusFailedPermanently},
	}
	for _, tc := range cases {
		payload := TransitionPayload{From: tc.from, To: tc.to}
		if RequiresFailureClassification(tc.to) {
			c := string(ClassUnknown)
			payload.FailureClassification = &c
		}
		if err := Lint(payload); err != nil {
			t.Fatalf("Lint(%s → %s) = %v, want nil", tc.from, tc.to, err)
		}
	}
}

func TestLintRejectsIllegalTransitions(t *testing.T) {
	illegal := []struct {
		from DeployStatus
		to   DeployStatus
	}{
		{StatusQueued, StatusBuilding},
		{StatusBuilding, StatusLive},
		{StatusLive, StatusFailed},        // final-from
		{StatusFailed, StatusQueued},      // final-from
		{StatusRolledBack, StatusQueued},  // final-from
		{StatusPreparing, StatusPreparing}, // self-loop
	}
	for _, tc := range illegal {
		payload := TransitionPayload{From: tc.from, To: tc.to}
		if RequiresFailureClassification(tc.to) {
			c := string(ClassUnknown)
			payload.FailureClassification = &c
		}
		err := Lint(payload)
		var typed ErrIllegalTransition
		if !errors.As(err, &typed) {
			t.Fatalf("Lint(%s → %s) error = %v, want ErrIllegalTransition", tc.from, tc.to, err)
		}
	}
}

func TestLintEnforcesClassificationOnFailureStates(t *testing.T) {
	cases := []struct {
		from DeployStatus
		to   DeployStatus
	}{
		{StatusBuilding, StatusFailed},
		{StatusCompensating, StatusRolledBack},
		{StatusCompensating, StatusFailedPermanently},
	}
	for _, tc := range cases {
		err := Lint(TransitionPayload{From: tc.from, To: tc.to})
		var typed ErrMissingClassification
		if !errors.As(err, &typed) {
			t.Fatalf("Lint(%s → %s) without classification = %v, want ErrMissingClassification", tc.from, tc.to, err)
		}
	}
}

func TestLintSkipsClassificationForCanceledAndLive(t *testing.T) {
	c := string(ClassUnknown)
	cases := []struct {
		from DeployStatus
		to   DeployStatus
	}{
		{StatusBuilding, StatusCanceled},
		{StatusSyncing, StatusLive},
	}
	for _, tc := range cases {
		if err := Lint(TransitionPayload{From: tc.from, To: tc.to}); err != nil {
			t.Fatalf("Lint(%s → %s) without classification: %v", tc.from, tc.to, err)
		}
		// extra classification is also fine.
		if err := Lint(TransitionPayload{From: tc.from, To: tc.to, FailureClassification: &c}); err != nil {
			t.Fatalf("Lint(%s → %s) with classification: %v", tc.from, tc.to, err)
		}
	}
}

func TestIsTerminalCoversAllFinalStatuses(t *testing.T) {
	for _, s := range AllFinalStatuses {
		if !IsTerminal(s) {
			t.Fatalf("IsTerminal(%s) = false, want true", s)
		}
	}
	if IsTerminal(StatusBuilding) {
		t.Fatalf("IsTerminal(building) = true, want false")
	}
}
