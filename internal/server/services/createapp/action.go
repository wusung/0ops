package createapp

import (
	"context"
)

// SideEffects computes side effects for app creation
func (s *Service) SideEffects(ctx context.Context, args *AppCreateArgs) ([]interface{}, error) {
	return nil, nil
}

// Precheck validates preconditions
func (s *Service) Precheck(ctx context.Context, args *AppCreateArgs) error {
	return nil
}

// Execute orchestrates app creation
func (s *Service) Execute(ctx context.Context, args *AppCreateArgs) (interface{}, error) {
	return nil, nil
}
