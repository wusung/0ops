package auth

import "context"

type contextKey string

const (
	keyActorUserID contextKey = "actor_user_id"
	keyTokenTeamID  contextKey = "token_team_id"
	keyTokenScopes  contextKey = "token_scopes"
	keyTeamID       contextKey = "team_id"
	keyTeamSlug     contextKey = "team_slug"
	keyActorRole    contextKey = "actor_role"
)

func withActorUserID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyActorUserID, value)
}

func withTokenTeamID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, keyTokenTeamID, value)
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

func ActorUserID(ctx context.Context) string {
	v, _ := ctx.Value(keyActorUserID).(string)
	return v
}

func TokenTeamID(ctx context.Context) string {
	v, _ := ctx.Value(keyTokenTeamID).(string)
	return v
}

func TokenScopes(ctx context.Context) []string {
	v, _ := ctx.Value(keyTokenScopes).([]string)
	return v
}

func TeamID(ctx context.Context) string {
	v, _ := ctx.Value(keyTeamID).(string)
	return v
}

func TeamSlug(ctx context.Context) string {
	v, _ := ctx.Value(keyTeamSlug).(string)
	return v
}

func ActorRole(ctx context.Context) string {
	v, _ := ctx.Value(keyActorRole).(string)
	return v
}
