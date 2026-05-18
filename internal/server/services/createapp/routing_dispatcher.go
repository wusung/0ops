package createapp

import (
	"context"
	"strings"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// RepoURLLookup resolves an app's stored repo_url for dispatcher routing.
// Implemented by db.Repository.GetAppRepoURLByTeamAndAppSlug.
type RepoURLLookup interface {
	GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error)
}

// RoutingDispatcher selects between GitHub and Local dispatchers based on the
// stored repo_url scheme. Per ADR-0012 § 3.2, the workflowdispatch.ClientPayload
// contract is preserved (no extra fields); routing reads from the database.
type RoutingDispatcher struct {
	GitHubDispatcher Dispatcher
	LocalDispatcher  Dispatcher
	Lookup           RepoURLLookup
}

func (r *RoutingDispatcher) Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error {
	url, err := r.Lookup.GetAppRepoURLByTeamAndAppSlug(ctx, payload.TeamSlug, payload.AppSlug)
	if err != nil {
		return err
	}
	if strings.HasPrefix(url, "file://") && r.LocalDispatcher != nil {
		return r.LocalDispatcher.Dispatch(ctx, payload)
	}
	if r.GitHubDispatcher == nil {
		// Mirror the pre-sub-spec nil-tolerant behaviour: when env vars for
		// production GHA dispatch are unset, the dispatcher is silently a
		// no-op rather than failing create_app confirm.
		return nil
	}
	return r.GitHubDispatcher.Dispatch(ctx, payload)
}
