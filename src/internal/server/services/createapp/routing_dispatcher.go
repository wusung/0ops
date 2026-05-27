package createapp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// RepoURLLookup resolves an app's stored repo_url for dispatcher routing.
// Implemented by db.Repository.GetAppRepoURLByTeamAndAppSlug.
type RepoURLLookup interface {
	GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error)
}

// RoutingDispatcher selects between GitHub, Local, and Upload dispatchers.
// Primary dispatch key is payload.SourceKind (populated by Service.Confirm, T14).
// Legacy URL-prefix lookup is preserved as a fallback for payloads that pre-date
// T14 (backward compat per ADR-0012 § 3.2).
type RoutingDispatcher struct {
	GitHubDispatcher Dispatcher
	LocalDispatcher  Dispatcher
	UploadDispatcher Dispatcher
	Lookup           RepoURLLookup
}

// Dispatch routes by SourceKind first, with URL-prefix lookup as legacy
// fallback. Nil-tolerance policy across kinds (symmetric by convention):
//   - UploadDispatcher nil → silent no-op + slog.Warn. Dev envs without
//     OPS_GITHUB_* env vars have a nil UploadGHADispatcher; upload source
//     create_app must succeed. Production misconfig is surfaced via Warn log
//     and boot-time env check (T4 AssertProductionSafe), not runtime error.
//   - GitHubDispatcher nil → silent no-op. Matches pre-T14 behaviour: dev
//     envs without OPS_GITHUB_* env vars expect create_app to succeed
//     without firing GHA, leaving downstream reconciler to drive state.
//   - LocalDispatcher nil → falls through to URL-prefix lookup. The legacy
//     path may have its own LocalDispatcher attached.
func (r *RoutingDispatcher) Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error {
	// Preferred path: source_kind populated by Service.Confirm (T14).
	switch payload.SourceKind {
	case "upload":
		if r.UploadDispatcher == nil {
			slog.Warn("routing_dispatcher: upload source with no UploadDispatcher configured; dispatch skipped",
				"team_slug", payload.TeamSlug, "app_slug", payload.AppSlug, "run_id", payload.RunID,
				"hint", "set OPS_GITHUB_* env in production or wire UploadGHADispatcher")
			return nil
		}
		return r.UploadDispatcher.Dispatch(ctx, payload)
	case "github":
		if r.GitHubDispatcher == nil {
			// nil-tolerant fallback — matches pre-T14 behavior for GitHub source.
			return nil
		}
		return r.GitHubDispatcher.Dispatch(ctx, payload)
	case "local":
		if r.LocalDispatcher != nil {
			return r.LocalDispatcher.Dispatch(ctx, payload)
		}
		// fall through to legacy URL lookup for safety
	}

	// Legacy / unflagged path: fall back to URL-prefix lookup. Preserves
	// backward compatibility for any payload built before T14 wired SourceKind.
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
