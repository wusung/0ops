package githubwebhook

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/redeploy"
)

// PushHandlerStore captures every DB call the push handler makes. Subset of
// *db.Repository; tests substitute a fake.
type PushHandlerStore interface {
	redeploy.TriggerStore
	FindTeamByGitHubInstallID(ctx context.Context, installID int64) (db.Team, error)
	FindLiveAppsByRepoAndBranch(ctx context.Context, teamID, repoURL, branch string) ([]db.App, error)
	HasInFlightDeployRun(ctx context.Context, appID string) (bool, error)
	AppendWebhookAudit(ctx context.Context, teamID, action string, args map[string]any, result map[string]any) error
}

// TriggerInvoker is the subset of *redeploy.Trigger the push handler uses
// — held as an interface so handler tests can substitute a recording
// implementation without spinning the dispatcher stack.
type TriggerInvoker interface {
	Trigger(ctx context.Context, args redeploy.TriggerArgs) (redeploy.TriggerResult, error)
}

// PushOutcome summarises what the handler did with a single push event,
// for logs / metrics / handler-response decoration. Acted == false with
// Skip non-empty means the handler intentionally did nothing (paused app,
// in-flight, branch filter, etc.) and still returns 200.
type PushOutcome struct {
	TeamID         string
	TeamSlug       string
	DeliveryID     string
	Triggered      []string // deploy_run ids
	Skipped        []SkipEntry
	Acted          bool
	Reason         string // top-level reason when Acted == false
}

// SkipEntry records an app that was matched but not re-deployed.
type SkipEntry struct {
	AppID   string
	AppSlug string
	Reason  string
}

// Skip reason constants. Stable strings so audit/log parsers can pivot.
const (
	SkipReasonPaused   = "paused"
	SkipReasonInFlight = "in_flight"
)

// PushHandler routes a single `push` event into one or more redeploy
// triggers (spec § 5.2).
type PushHandler struct {
	store   PushHandlerStore
	trigger TriggerInvoker
}

// NewPushHandler wires the dependencies.
func NewPushHandler(store PushHandlerStore, trigger TriggerInvoker) *PushHandler {
	return &PushHandler{store: store, trigger: trigger}
}

// Handle parses the payload, applies the spec § 5.2 routing pipeline, and
// returns the outcome. Callers MUST have already verified HMAC + dedup
// before invoking Handle (spec § 14 hard rule #10).
func (h *PushHandler) Handle(ctx context.Context, deliveryID, traceID string, payload []byte) (PushOutcome, error) {
	out := PushOutcome{DeliveryID: deliveryID}

	event, err := ParsePushPayload(payload)
	if err != nil {
		return out, fmt.Errorf("decode push payload: %w", err)
	}
	if event.Installation.ID == 0 {
		out.Reason = "missing_installation_id"
		return out, nil
	}
	if event.Deleted {
		out.Reason = "branch_deleted"
		return out, nil
	}
	branch, ok := BranchFromRef(event.Ref)
	if !ok {
		out.Reason = "non_branch_ref"
		return out, nil
	}

	team, err := h.store.FindTeamByGitHubInstallID(ctx, event.Installation.ID)
	if err != nil {
		if errors.Is(err, db.ErrTeamNotFound) {
			out.Reason = "team_not_found"
			return out, nil
		}
		return out, fmt.Errorf("find team: %w", err)
	}
	out.TeamID = team.ID
	out.TeamSlug = team.Slug

	repoURL := NormalizeRepoURL(event.Repository.HTMLURL)
	apps, err := h.store.FindLiveAppsByRepoAndBranch(ctx, team.ID, repoURL, branch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			out.Reason = "no_matching_app"
			return out, nil
		}
		return out, fmt.Errorf("find apps: %w", err)
	}
	if len(apps) == 0 {
		out.Reason = "no_matching_app"
		_ = h.store.AppendWebhookAudit(ctx, team.ID, "github_webhook_push_no_match", map[string]any{
			"delivery_id": deliveryID,
			"repo_url":    repoURL,
			"branch":      branch,
		}, map[string]any{"status": "ignored"})
		return out, nil
	}

	for _, app := range apps {
		if app.Status != nil && *app.Status == "paused" {
			out.Skipped = append(out.Skipped, SkipEntry{AppID: app.ID, AppSlug: app.Slug, Reason: SkipReasonPaused})
			_ = h.store.AppendWebhookAudit(ctx, team.ID, "github_webhook_push_skipped", map[string]any{
				"delivery_id": deliveryID,
				"app_id":      app.ID,
				"app_slug":    app.Slug,
				"reason":      SkipReasonPaused,
			}, map[string]any{"status": "skipped"})
			continue
		}
		busy, err := h.store.HasInFlightDeployRun(ctx, app.ID)
		if err != nil {
			return out, fmt.Errorf("check in-flight for %s: %w", app.Slug, err)
		}
		if busy {
			out.Skipped = append(out.Skipped, SkipEntry{AppID: app.ID, AppSlug: app.Slug, Reason: SkipReasonInFlight})
			_ = h.store.AppendWebhookAudit(ctx, team.ID, "github_webhook_push_skipped", map[string]any{
				"delivery_id": deliveryID,
				"app_id":      app.ID,
				"app_slug":    app.Slug,
				"reason":      SkipReasonInFlight,
			}, map[string]any{"status": "skipped"})
			continue
		}
		result, err := h.trigger.Trigger(ctx, redeploy.TriggerArgs{
			TeamID:            team.ID,
			TeamSlug:          team.Slug,
			AppID:             app.ID,
			AppSlug:           app.Slug,
			RepoURL:           repoURL,
			Ref:               branch,
			CommitSHA:         event.After,
			Source:            redeploy.SourceWebhook,
			WebhookDeliveryID: deliveryID,
			TraceID:           traceID,
		})
		if err != nil {
			return out, fmt.Errorf("trigger redeploy for %s: %w", app.Slug, err)
		}
		out.Triggered = append(out.Triggered, result.DeployRunID)
		_ = h.store.AppendWebhookAudit(ctx, team.ID, "github_webhook_push_triggered", map[string]any{
			"delivery_id":   deliveryID,
			"app_id":        app.ID,
			"app_slug":      app.Slug,
			"commit_sha":    event.After,
			"branch":        branch,
			"deploy_run_id": result.DeployRunID,
		}, map[string]any{"status": "queued"})
	}
	out.Acted = len(out.Triggered) > 0
	return out, nil
}
