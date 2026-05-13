package createapp

import (
	"testing"
)

func TestStateMachineTransitions(t *testing.T) {
	// Test happy path
	sm := NewStateMachine(StateQueued)
	path := []string{StatePreparing, StateBuilding, StatePushing, StateRendering, StateSyncing, StateLive}
	for _, state := range path {
		if err := sm.Transition(state); err != nil {
			t.Fatalf("happy path failed at %s: %v", state, err)
		}
	}
	if sm.CurrentState() != StateLive {
		t.Errorf("final state = %s, want %s", sm.CurrentState(), StateLive)
	}

	// Test compensation path
	sm = NewStateMachine(StateQueued)
	if err := sm.Transition(StatePreparing); err != nil {
		t.Fatalf("transition to preparing failed: %v", err)
	}
	if err := sm.Transition(StateCompensating); err != nil {
		t.Fatalf("transition to compensating failed: %v", err)
	}
	if err := sm.Transition(StateRolledBack); err != nil {
		t.Fatalf("transition to rolled_back failed: %v", err)
	}

	// Test invalid: skip state
	sm = NewStateMachine(StateQueued)
	if err := sm.Transition(StateBuilding); err == nil {
		t.Errorf("expected error when skipping prepare state, but got nil")
	}

	// Test invalid: transition from terminal state
	sm = NewStateMachine(StateLive)
	if err := sm.Transition(StatePreparing); err == nil {
		t.Errorf("expected error transitioning from live state, but got nil")
	}
}

func TestStateMachineIsTerminal(t *testing.T) {
	tests := []struct {
		state      string
		wantTerminal bool
	}{
		{StateQueued, false},
		{StatePreparing, false},
		{StateBuilding, false},
		{StateLive, true},
		{StateRolledBack, true},
		{StateFailed, true},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			sm := NewStateMachine(tt.state)
			if sm.IsTerminal() != tt.wantTerminal {
				t.Errorf("IsTerminal() = %v, want %v", sm.IsTerminal(), tt.wantTerminal)
			}
		})
	}
}
