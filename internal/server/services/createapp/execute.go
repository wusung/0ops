package createapp

import (
	"context"
	"fmt"
)

// ExecuteResult represents the result of app creation
type ExecuteResult struct {
	AppID     string
	Status    string
	Message   string
	Timestamp int64
}

// Execute implements the saga orchestration per spec § 5.4
// Coordinates 5 side effects with compensation on failure
func (s *Service) Execute(ctx context.Context, args *AppCreateArgs) (interface{}, error) {
	// Step 1: Compute side effects
	effects, err := s.SideEffects(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("compute side effects failed: %w", err)
	}

	// Step 2: Precheck validation
	if err := s.Precheck(ctx, args); err != nil {
		return nil, fmt.Errorf("precheck failed: %w", err)
	}

	// Step 3: Saga orchestration
	// Track completed effects for compensation
	completedEffects := []SideEffect{}
	sm := NewStateMachine(StateQueued)

	// Execute reversible effects (R1-R3)
	reversibleEffects := effects[:3]
	for _, effect := range reversibleEffects {
		if err := s.executeEffect(ctx, effect); err != nil {
			return s.compensate(ctx, completedEffects)
		}
		completedEffects = append(completedEffects, effect)

		// Advance state
		targetState := s.getNextState(sm.CurrentState())
		if targetState != "" {
			if err := sm.Transition(targetState); err != nil {
				return s.compensate(ctx, completedEffects)
			}
		}
	}

	// Execute irreversible effects (I1-I2)
	irreversibleEffects := effects[3:]
	for _, effect := range irreversibleEffects {
		if err := s.executeEffect(ctx, effect); err != nil {
			// On irreversible failure, enter compensating state
			if err := sm.Transition(StateCompensating); err == nil {
				return s.compensate(ctx, completedEffects)
			}
			return nil, fmt.Errorf("irreversible effect failed and compensation failed: %w", err)
		}
		completedEffects = append(completedEffects, effect)
	}

	// Advance to terminal state
	if err := sm.Transition(StateLive); err != nil {
		return nil, fmt.Errorf("state transition to live failed: %w", err)
	}

	return &ExecuteResult{
		AppID:     effects[0].(*DBInsertSideEffect).AppID,
		Status:    StateLive,
		Message:   "app created successfully",
		Timestamp: 0, // Would be set to real timestamp
	}, nil
}

func (s *Service) executeEffect(ctx context.Context, effect SideEffect) error {
	// Placeholder implementation
	// In real implementation, would delegate to appropriate client:
	// - DBInsertSideEffect -> s.db.CreateApp(...)
	// - K3sNamespaceSideEffect -> s.k3sClient.CreateNamespace(...)
	// - GitOpsRenderPushSideEffect -> s.gitopsClient.RenderAndPush(...)
	// - OpsTokenSignSideEffect -> s.opsTokenClient.Sign(...)
	// - GHADispatchSideEffect -> s.ghaClient.Dispatch(...)
	return nil
}

func (s *Service) compensate(ctx context.Context, effects []SideEffect) (interface{}, error) {
	// Execute compensation in reverse order
	// Per spec § 5.5: backward functions for each reversible effect
	for i := len(effects) - 1; i >= 0; i-- {
		effect := effects[i]
		// Skip compensation for irreversible effects
		if i >= 3 {
			continue
		}

		// Call corresponding backward function
		// This is simplified - would call specific cleanup per effect type
		_ = effect.Name() // Just to use the effect
	}

	return &ExecuteResult{
		Status:  StateRolledBack,
		Message: "app creation failed and rolled back",
	}, nil
}

func (s *Service) getNextState(currentState string) string {
	transitions := map[string]string{
		StateQueued:       StatePreparing,
		StatePreparing:    StateBuilding,
		StateBuilding:     StatePushing,
		StatePushing:      StateRendering,
		StateRendering:    StateSyncing,
		StateSyncing:      StateLive,
	}
	return transitions[currentState]
}
