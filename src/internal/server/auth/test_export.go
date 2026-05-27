package auth

import "context"

// WithTokenIDForTest is a test-only helper to inject a token id into ctx.
//
//nolint:revive // exported for cross-package tests
func WithTokenIDForTest(ctx context.Context, id string) context.Context {
	return withTokenID(ctx, id)
}

// WithTeamIDForTest is a test-only helper to inject a team id into ctx.
//
//nolint:revive // exported for cross-package tests
func WithTeamIDForTest(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyTeamID, id)
}

// WithTeamPlanForTest is a test-only helper to inject a plan tier into ctx.
//
//nolint:revive // exported for cross-package tests
func WithTeamPlanForTest(ctx context.Context, plan string) context.Context {
	return withTeamPlan(ctx, plan)
}
