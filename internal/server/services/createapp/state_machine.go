package createapp

import "fmt"

// Deploy_run states per spec § 7.2
const (
	StateQueued       = "queued"
	StatePreparing    = "preparing"
	StateBuilding     = "building"
	StatePushing      = "pushing"
	StateRendering    = "rendering"
	StateSyncing      = "syncing"
	StateLive         = "live"
	StateCompensating = "compensating"
	StateRolledBack   = "rolled_back"
	StateFailed       = "failed"
)

type StateMachine struct {
	currentState string
}

func NewStateMachine(state string) *StateMachine {
	return &StateMachine{currentState: state}
}

func (sm *StateMachine) CurrentState() string {
	return sm.currentState
}

// legalTransitions defines allowed state transitions
var legalTransitions = map[string][]string{
	StateQueued: {StatePreparing},
	StatePreparing: {StateBuilding, StateCompensating},
	StateBuilding: {StatePushing, StateCompensating},
	StatePushing: {StateRendering, StateCompensating},
	StateRendering: {StateSyncing, StateCompensating},
	StateSyncing: {StateLive, StateCompensating},
	StateLive: {}, // Terminal state
	StateCompensating: {StateRolledBack, StateFailed},
	StateRolledBack: {}, // Terminal state
	StateFailed: {}, // Terminal state
}

// Transition validates and applies a state transition
func (sm *StateMachine) Transition(targetState string) error {
	allowed, exists := legalTransitions[sm.currentState]
	if !exists {
		return fmt.Errorf("unknown current state: %s", sm.currentState)
	}

	// Check if transition is allowed
	for _, s := range allowed {
		if s == targetState {
			sm.currentState = targetState
			return nil
		}
	}

	return fmt.Errorf("invalid transition: %s -> %s", sm.currentState, targetState)
}

// IsTerminal checks if the current state is terminal
func (sm *StateMachine) IsTerminal() bool {
	_, exists := legalTransitions[sm.currentState]
	if !exists {
		return false
	}
	return len(legalTransitions[sm.currentState]) == 0
}
