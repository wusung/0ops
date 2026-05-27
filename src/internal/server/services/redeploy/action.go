package redeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// PreviewAction is the canonical preview.action value persisted on the
// preview row for user-initiated redeploys (spec § 6 + MCP lint § 4.3).
const PreviewAction = "redeploy"

// Sentinel errors surfaced to the handler layer for class mapping. Kept
// distinct so the handler can return precise apperror codes (spec § 9
// `error-model` row).
var (
	ErrAppNotFound      = errors.New("app not found")
	ErrAppPaused        = errors.New("app paused")
	ErrAppBusy          = errors.New("app has in-flight deploy")
	ErrPreviewNotFound  = errors.New("preview not found")
	ErrPreviewConsumed  = errors.New("preview already consumed")
	ErrPreviewExpired   = errors.New("preview expired")
	ErrValidationFailed = errors.New("validation failed")
)

// Store captures the DB queries the redeploy preview/confirm path needs.
// Implemented by *db.Repository + reused in the createapp fake store.
type Store interface {
	TriggerStore
	GetTeamAppBySlug(ctx context.Context, teamID, appSlug string) (db.App, error)
	HasInFlightDeployRun(ctx context.Context, appID string) (bool, error)
	CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error
}

// Service exposes Preview + Confirm endpoints for user-initiated redeploys.
type Service struct {
	store   Store
	trigger *Trigger
	now     func() time.Time
}

// New wires a Service. `trigger` must be non-nil; nil dispatcher inside
// Trigger is allowed for dev environments.
func New(store Store, trigger *Trigger) *Service {
	return &Service{store: store, trigger: trigger, now: time.Now}
}

// PreviewResult is the lightweight projection of preview metadata that the
// handler returns to the caller alongside the side_effects list.
type PreviewResult struct {
	PreviewID   string
	Summary     string
	ExpiresAt   time.Time
	SideEffects []string
}

// ConfirmResult is the durable redeploy response plus the
// idempotent-replay flag the handler uses for metric outcome labelling.
type ConfirmResult struct {
	Response         dto.RedeployResponse
	Replayed         bool
	PreviewCreatedAt time.Time
}

// previewArgs is the persistent shape stored in preview.args; it captures
// just enough to replay the action without re-fetching from GitHub.
type previewArgs struct {
	AppSlug   string `json:"app_slug"`
	AppID     string `json:"app_id"`
	Ref       string `json:"ref"`
	CommitSHA string `json:"commit_sha"`
}

// Preview opens a redeploy preview for `appSlug` on behalf of the actor.
// Returns ErrAppNotFound if the slug does not resolve in this team and
// ErrAppPaused if the app is paused (spec § 7 row 2).
func (s *Service) Preview(ctx context.Context, teamID, actorUserID, teamSlug, appSlug string, args dto.RedeployRequest) (PreviewResult, error) {
	if teamID == "" || actorUserID == "" {
		return PreviewResult{}, ErrValidationFailed
	}
	app, err := s.lookupActiveApp(ctx, teamID, appSlug)
	if err != nil {
		return PreviewResult{}, err
	}

	commitSHA := strings.TrimSpace(strPtr(args.CommitSHA))
	ref := strings.TrimSpace(strPtr(args.Ref))
	if ref == "" && app.RepoDefaultBranch != nil {
		ref = strings.TrimSpace(*app.RepoDefaultBranch)
	}
	if ref == "" {
		ref = "main"
	}
	if commitSHA == "" {
		// Spec § 6.2 step 2: backend should ask GitHub for HEAD when caller
		// omits commit_sha. v1 falls back to the ref string so preview can
		// be served without an installation token in dev; reconciler will
		// reconcile once the callback lands.
		commitSHA = ref
	}

	payload := previewArgs{
		AppSlug:   app.Slug,
		AppID:     app.ID,
		Ref:       ref,
		CommitSHA: commitSHA,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return PreviewResult{}, err
	}

	short := commitSHA
	if len(short) > 7 {
		short = short[:7]
	}
	summary := fmt.Sprintf("Redeploy app %q (commit %s, ref %s)", app.Slug, short, ref)
	row, err := s.store.CreatePreview(ctx, teamID, actorUserID, PreviewAction, raw, summary)
	if err != nil {
		return PreviewResult{}, err
	}

	return PreviewResult{
		PreviewID: row.ID,
		Summary:   summary,
		ExpiresAt: row.ExpiresAt,
		SideEffects: []string{
			fmt.Sprintf("INSERT deploy_run for app %q (commit %s)", app.Slug, short),
			"Sign ops_token for GitHub Actions workflow",
			fmt.Sprintf("Trigger workflow_dispatch on %s @ %s", strPtr(app.RepoURL), ref),
			"ArgoCD will sync the new image once build completes",
		},
	}, nil
}

// Confirm consumes the preview and (on first call) executes Trigger.
// Idempotent replay returns the persisted last_result. An in-flight deploy
// returns ErrAppBusy so the handler can map to 409 app_busy.
func (s *Service) Confirm(ctx context.Context, teamID, actorUserID, teamSlug, previewID, traceID string) (ConfirmResult, error) {
	preview, err := s.store.GetPreview(ctx, previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			return ConfirmResult{}, ErrPreviewNotFound
		}
		return ConfirmResult{}, err
	}
	if preview.Action != PreviewAction || preview.TeamID != teamID || preview.ActorUserID != actorUserID {
		return ConfirmResult{}, ErrPreviewNotFound
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			return ConfirmResult{}, ErrPreviewConsumed
		}
		var response dto.RedeployResponse
		if err := json.Unmarshal(preview.LastResult, &response); err != nil {
			return ConfirmResult{}, fmt.Errorf("decode last_result: %w", err)
		}
		return ConfirmResult{Response: response, Replayed: true, PreviewCreatedAt: preview.CreatedAt}, nil
	}
	if preview.ExpiresAt.Before(s.now().UTC()) {
		return ConfirmResult{}, ErrPreviewExpired
	}

	var args previewArgs
	if err := json.Unmarshal(preview.Args, &args); err != nil {
		return ConfirmResult{}, fmt.Errorf("decode preview args: %w", err)
	}

	app, err := s.lookupActiveApp(ctx, teamID, args.AppSlug)
	if err != nil {
		return ConfirmResult{}, err
	}

	busy, err := s.store.HasInFlightDeployRun(ctx, app.ID)
	if err != nil {
		return ConfirmResult{}, fmt.Errorf("check in-flight: %w", err)
	}
	if busy {
		return ConfirmResult{}, ErrAppBusy
	}

	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = preview.ID
	}

	trigger, err := s.trigger.Trigger(ctx, TriggerArgs{
		TeamID:      teamID,
		TeamSlug:    teamSlug,
		AppID:       app.ID,
		AppSlug:     app.Slug,
		RepoURL:     strPtr(app.RepoURL),
		Ref:         args.Ref,
		CommitSHA:   args.CommitSHA,
		Source:      SourceUser,
		ActorUserID: actorUserID,
		TraceID:     traceID,
	})
	if err != nil {
		return ConfirmResult{}, err
	}

	response := dto.RedeployResponse{
		AppID:         app.ID,
		AppSlug:       app.Slug,
		DeployRunID:   trigger.DeployRunID,
		TraceID:       trigger.TraceID,
		CommitSHA:     trigger.CommitSHA,
		Ref:           args.Ref,
		Source:        string(SourceUser),
		SubdomainURL:  trigger.SubdomainURL,
		InitialDeploy: false,
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return ConfirmResult{}, err
	}
	if err := s.store.ConsumePreviewWithResult(ctx, preview.ID, encoded); err != nil {
		if errors.Is(err, db.ErrPreviewConsumed) {
			return ConfirmResult{}, ErrPreviewConsumed
		}
		return ConfirmResult{}, err
	}
	return ConfirmResult{Response: response, PreviewCreatedAt: preview.CreatedAt}, nil
}

// lookupActiveApp returns the team-scoped app row if it exists and is not
// paused (spec § 7).
func (s *Service) lookupActiveApp(ctx context.Context, teamID, appSlug string) (db.App, error) {
	app, err := s.store.GetTeamAppBySlug(ctx, teamID, appSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.App{}, ErrAppNotFound
		}
		return db.App{}, err
	}
	if app.Status != nil && *app.Status == "paused" {
		return db.App{}, ErrAppPaused
	}
	return app, nil
}

func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
