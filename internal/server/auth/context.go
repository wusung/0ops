// Package auth provides authentication context utilities.
package auth

import "context"

type contextKey string

const (
	keyActorUserID contextKey = "actor_user_id"
	keyTokenID     contextKey = "token_id"
	keyTokenTeamID contextKey = "token_team_id"
	keyTokenKind   contextKey = "token_kind"
	keyTokenScopes contextKey = "token_scopes"
	keyTeamID      contextKey = "team_id"
	keyTeamSlug    contextKey = "team_slug"
	keyActorRole   contextKey = "actor_role"
)

func withActorUserID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyActorUserID, value)
}

func withTokenTeamID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyTokenTeamID, value)
}

func withTokenID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyTokenID, value)
}

func withTokenKind(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyTokenKind, value)
}

func withTokenScopes(ctx context.Context, value []string) context.Context {
	return context.WithValue(ctx, keyTokenScopes, value)
}

func withTeam(ctx context.Context, id, slug string) context.Context {
	ctx = context.WithValue(ctx, keyTeamID, id)
	return context.WithValue(ctx, keyTeamSlug, slug)
}

func withActorRole(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyActorRole, value)
}

//nolint:revive // exported for public API
func ActorUserID(ctx context.Context) string {
	v, _ := ctx.Value(keyActorUserID).(string)
	return v
}

//nolint:revive // exported for public API
func TokenTeamID(ctx context.Context) string {
	v, _ := ctx.Value(keyTokenTeamID).(string)
	return v
}

//nolint:revive // exported for public API
func TokenID(ctx context.Context) string {
	v, _ := ctx.Value(keyTokenID).(string)
	return v
}

//nolint:revive // exported for public API
func TokenKind(ctx context.Context) string {
	v, _ := ctx.Value(keyTokenKind).(string)
	return v
}

//nolint:revive // exported for public API
func TokenScopes(ctx context.Context) []string {
	v, _ := ctx.Value(keyTokenScopes).([]string)
	return v
}

//nolint:revive // exported for public API
func TeamID(ctx context.Context) string {
	v, _ := ctx.Value(keyTeamID).(string)
	return v
}

//nolint:revive // exported for public API
func TeamSlug(ctx context.Context) string {
	v, _ := ctx.Value(keyTeamSlug).(string)
	return v
}

//nolint:revive // exported for public API
func ActorRole(ctx context.Context) string {
	v, _ := ctx.Value(keyActorRole).(string)
	return v
}
