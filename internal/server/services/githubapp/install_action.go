package githubapp

import (
	"context"
	"fmt"
	"time"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
)

// InstallGitHubAppAction implements install_github_app action for preview/confirm gate.
type InstallGitHubAppAction struct {
	store        InstallStore
	stateSigner  *StateSigner
	jwtSigner    *JWTSigner
	clientID     string
	redirectURI  string
	githubAppID  int64
	githubAppURL string
}

// InstallStore defines query interface for install action.
type InstallStore interface {
	GetTeam(ctx context.Context, slug string) (*db.Team, error)
}

// PreviewArgs for install_github_app:preview request body.
type PreviewArgs struct {
	// empty for now; all context comes from URL + auth middleware
}

// ConfirmArgs for install_github_app confirm request body.
type ConfirmArgs struct {
	PreviewID string `json:"preview_id"`
}

// InstallResult returned by confirm action.
type InstallResult struct {
	InstallURL string `json:"install_url"`
}

// SideEffect type for action framework.
type SideEffect struct {
	Description string
	Resource    string
	Reversible  bool
}

// NewInstallGitHubAppAction creates installer action.
func NewInstallGitHubAppAction(
	store InstallStore,
	stateSigner *StateSigner,
	jwtSigner *JWTSigner,
	clientID string,
	redirectURI string,
	githubAppID int64,
	githubAppURL string,
) *InstallGitHubAppAction {
	return &InstallGitHubAppAction{
		store:        store,
		stateSigner:  stateSigner,
		jwtSigner:    jwtSigner,
		clientID:     clientID,
		redirectURI:  redirectURI,
		githubAppID:  githubAppID,
		githubAppURL: githubAppURL,
	}
}

// Name returns action name for RBAC registration.
func (a *InstallGitHubAppAction) Name() string {
	return "install_github_app"
}

// SideEffects computes dry-run side effects for preview.
// For install, there are no reversible side effects during preview.
func (a *InstallGitHubAppAction) SideEffects(_ context.Context, _ any) (string, []SideEffect, error) {
	// No side effects for preview; the real mutation happens on confirm.
	return "Will install GitHub App", []SideEffect{}, nil
}

// Execute runs the confirm action (generates install URL with signed state).
func (a *InstallGitHubAppAction) Execute(ctx context.Context, _ any, _ []SideEffect) (any, error) {
	teamID := auth.TeamID(ctx)
	actorUserID := auth.ActorUserID(ctx)

	if teamID == "" || actorUserID == "" {
		return nil, fmt.Errorf("missing team_id or actor_user_id in context")
	}

	// Generate preview_id as state token key
	// Note: In real preview-confirm gate, this would be passed via request body
	// For now we generate a new one as part of execute
	previewID := fmt.Sprintf("preview_%d_%d_%d", time.Now().Unix(), len(teamID), len(actorUserID))

	state, err := a.stateSigner.SignState(teamID, actorUserID, previewID)
	if err != nil {
		return nil, fmt.Errorf("failed to sign state: %w", err)
	}

	// Construct GitHub App install URL
	// GitHub App install flow: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#web-application-flow
	installURL := fmt.Sprintf(
		"%s/apps/0ops/installations/new?state=%s",
		a.githubAppURL,
		state,
	)

	return InstallResult{
		InstallURL: installURL,
	}, nil
}

// Precheck verifies preconditions within confirm transaction.
func (a *InstallGitHubAppAction) Precheck(ctx context.Context, _ any) error {
	teamID := auth.TeamID(ctx)
	if teamID == "" {
		return fmt.Errorf("team_id missing in context")
	}

	// Fetch team to verify it exists and user has access
	team, err := a.store.GetTeam(ctx, teamID)
	if err != nil {
		return fmt.Errorf("failed to fetch team: %w", err)
	}
	if team == nil {
		return fmt.Errorf("team not found")
	}

	return nil
}
