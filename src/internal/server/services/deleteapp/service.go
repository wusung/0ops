package deleteapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	gitopssvc "github.com/winshare/zeroops/internal/server/services/gitops"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// Store captures the db operations required by the delete_app saga.
type Store interface {
	GetTeamAppBySlug(ctx context.Context, teamID, slug string) (db.App, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, actionSummary string) (db.Preview, error)
	ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error
	ListAppDomainBindings(ctx context.Context, appID string) ([]db.AppDomainBinding, error)
	DeleteAppDomainBindings(ctx context.Context, appID string) error
	ListInFlightDeployRuns(ctx context.Context, appID string) ([]db.InFlightDeployRun, error)
	CancelDeployRun(ctx context.Context, runID, reason string) error
	UpdateAppStatus(ctx context.Context, appID, status string) error
	DeleteAppByID(ctx context.Context, appID string) error
	EnqueueReconciliationJob(ctx context.Context, in db.ReconciliationJobInsert) (string, error)
	AppendAuditLog(ctx context.Context, in db.AuditLogInsert) error
	MarkReconciliationJobAttempt(ctx context.Context, jobID, lastErr string, nextAt *time.Time, completed bool) error
}

// WorkflowCanceler best-effort cancels an in-flight GitHub Actions workflow
// run. delete_app does not require this to succeed (audit + cleanup_residue
// will catch the orphan), but a real implementation should call the
// `POST /repos/{owner}/{repo}/actions/runs/{run_id}/cancel` endpoint.
type WorkflowCanceler interface {
	CancelWorkflowRun(ctx context.Context, runID int64) error
}

// CustomHostnameClient releases Cloudflare custom hostnames during the
// reversible phase (spec § 5.2 R2). The interface mirrors the one already
// used by domainverify.
type CustomHostnameClient interface {
	DeleteCustomHostname(ctx context.Context, cfHostnameID string) error
}

// GitOpsRemover removes an app directory from the 0ops-gitops repo and
// pushes the change (spec § 5.2 R4). Production wiring uses an extension
// of gitopssvc.Service; tests can supply a fake.
type GitOpsRemover interface {
	RemoveAppDir(ctx context.Context, input GitOpsRemoveInput) (gitopssvc.GitOpsResult, error)
}

// GitOpsRemoveInput is the rendering contract for an app deletion commit.
type GitOpsRemoveInput struct {
	TeamSlug    string
	AppSlug     string
	PreviewID   string
	TraceID     string
	DeleteRunID string
}

// ArgoCDChecker reports whether ArgoCD still has the app's Application
// resource. The cleanup_residue handler polls this until the resource is
// gone (or the timeout fires) before hard-deleting the app row.
type ArgoCDChecker interface {
	ApplicationExists(ctx context.Context, teamSlug, appSlug string) (bool, error)
}

// Service orchestrates delete_app preview and confirm.
type Service struct {
	store          Store
	cf             CustomHostnameClient
	workflow       WorkflowCanceler
	gitops         GitOpsRemover
	argoCD         ArgoCDChecker
	residueTimeout time.Duration
	now            func() time.Time
}

// New returns a delete_app orchestration service. Any dependency may be
// nil; missing dependencies become best-effort no-ops (the side effect is
// still recorded for cleanup_residue to retry).
func New(store Store, cf CustomHostnameClient, workflow WorkflowCanceler, gitops GitOpsRemover, argoCD ArgoCDChecker) *Service {
	return &Service{
		store:          store,
		cf:             cf,
		workflow:       workflow,
		gitops:         gitops,
		argoCD:         argoCD,
		residueTimeout: 5 * time.Minute,
		now:            func() time.Time { return time.Now().UTC() },
	}
}

// WithClock overrides the time source; used by tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// WithResidueTimeout overrides the cleanup_residue timeout; used by tests.
func (s *Service) WithResidueTimeout(d time.Duration) *Service {
	s.residueTimeout = d
	return s
}

// PreviewResult is the durable preview response plus the side_effects list.
type PreviewResult struct {
	Preview     db.Preview
	Summary     string
	SideEffects []dto.SideEffect
}

// Preview validates the args, computes side_effects, and persists the
// preview row. The caller maps typed errors onto apperror codes.
func (s *Service) Preview(ctx context.Context, teamID, actorUserID, teamSlug, appSlug, confirm string) (PreviewResult, error) {
	if err := ValidateArgs(appSlug, confirm); err != nil {
		return PreviewResult{}, err
	}
	app, err := s.store.GetTeamAppBySlug(ctx, teamID, appSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreviewResult{}, ErrAppNotFound
		}
		return PreviewResult{}, err
	}
	if app.Status != nil && *app.Status == "deleting" {
		return PreviewResult{}, ErrAlreadyDeleting
	}
	bindings, err := s.store.ListAppDomainBindings(ctx, app.ID)
	if err != nil {
		return PreviewResult{}, err
	}
	sideEffects := SideEffectsForApp(bindings)
	summary := SummaryForApp(teamSlug, appSlug)

	args, err := json.Marshal(dto.AppDeleteRequest{Confirm: confirm})
	if err != nil {
		return PreviewResult{}, err
	}
	preview, err := s.store.CreatePreview(ctx, teamID, actorUserID, PreviewAction, args, summary)
	if err != nil {
		return PreviewResult{}, err
	}

	// Audit row records the preview intent. Failure here is non-fatal —
	// the saga still proceeds; cleanup_residue does not depend on the row.
	subjectID := app.ID
	previewID := preview.ID
	_ = s.store.AppendAuditLog(ctx, db.AuditLogInsert{
		TeamID:      teamID,
		ActorUserID: ptr(actorUserID),
		SubjectType: SubjectType,
		SubjectID:   &subjectID,
		Action:      "delete_app_preview",
		Args:        map[string]any{"app_slug": appSlug, "confirm": confirm},
		Result:      map[string]any{"status": "ok"},
		PreviewID:   &previewID,
	})

	return PreviewResult{Preview: preview, Summary: summary, SideEffects: sideEffects}, nil
}

// ConfirmResult is the durable delete_app response plus replay metadata.
type ConfirmResult struct {
	Response         dto.AppDeleteResponse
	Replayed         bool
	PreviewCreatedAt time.Time
}

// Confirm executes the reversible phase of the saga. On success it returns
// the durable response and enqueues a cleanup_residue reconciliation_job;
// on failure mid-reversible it marks the app delete_compensated and
// enqueues cleanup_residue for the partial state.
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
		var response dto.AppDeleteResponse
		if err := json.Unmarshal(preview.LastResult, &response); err != nil {
			return ConfirmResult{}, fmt.Errorf("decode replay result: %w", err)
		}
		return ConfirmResult{Response: response, Replayed: true, PreviewCreatedAt: preview.CreatedAt}, nil
	}
	if preview.ExpiresAt.Before(s.now()) {
		return ConfirmResult{}, ErrPreviewExpired
	}

	var args dto.AppDeleteRequest
	if err := json.Unmarshal(preview.Args, &args); err != nil {
		return ConfirmResult{}, fmt.Errorf("decode preview args: %w", err)
	}

	app, err := s.store.GetTeamAppBySlug(ctx, teamID, args.Confirm)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfirmResult{}, ErrAppNotFound
		}
		return ConfirmResult{}, err
	}
	if app.Status != nil && *app.Status == "deleting" {
		// Another delete already advanced the row; treat as a replay-on-conflict.
		return ConfirmResult{}, ErrAlreadyDeleting
	}

	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = preview.ID
	}

	if err := s.executeReversible(ctx, executeContext{
		teamID:      teamID,
		teamSlug:    teamSlug,
		appID:       app.ID,
		appSlug:     args.Confirm,
		previewID:   preview.ID,
		traceID:     traceID,
		actorUserID: actorUserID,
	}); err != nil {
		return ConfirmResult{}, err
	}

	deletedAt := s.now().Format(time.RFC3339)
	response := dto.AppDeleteResponse{
		AppID:     app.ID,
		AppSlug:   args.Confirm,
		DeletedAt: deletedAt,
		TraceID:   traceID,
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return ConfirmResult{}, err
	}
	if err := s.store.ConsumePreviewWithResult(ctx, preview.ID, responseJSON); err != nil {
		if errors.Is(err, db.ErrPreviewConsumed) {
			return ConfirmResult{}, ErrPreviewConsumed
		}
		return ConfirmResult{}, err
	}

	previewIDLocal := preview.ID
	traceIDLocal := traceID
	appIDLocal := app.ID
	_ = s.store.AppendAuditLog(ctx, db.AuditLogInsert{
		TeamID:      teamID,
		ActorUserID: ptr(actorUserID),
		SubjectType: SubjectType,
		SubjectID:   &appIDLocal,
		Action:      "delete_app_confirm",
		Args:        map[string]any{"app_slug": args.Confirm},
		Result:      map[string]any{"status": "deleting", "deleted_at": deletedAt},
		PreviewID:   &previewIDLocal,
		TraceID:     &traceIDLocal,
	})

	return ConfirmResult{Response: response, PreviewCreatedAt: preview.CreatedAt}, nil
}

func ptr[T any](v T) *T { return &v }
