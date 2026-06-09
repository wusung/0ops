package createapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
	gitopssvc "github.com/winshare/zeroops/internal/server/services/gitops"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
	"github.com/winshare/zeroops/internal/shared/dto"
)

const previewAction = "create_app"
const tunnelBackendURL = "http://traefik.kube-system.svc.cluster.local:80"

// uploadPinTTL is the placeholder expiry applied to an upload row at create_app
// confirm time. Spec §10 specifies the canonical pinned expiry as
// "deploy_run.terminal_at + 7d", but at confirm time deploy_run.terminal_at
// is NULL — the deploy has only just started. The T19 GC reconciler (or a
// future terminal-state reconciler) should re-pin with the canonical value
// when the deploy_run transitions to a terminal state. Until that lands,
// 30 days is generous enough to cover any realistic deploy duration while
// still bounding orphan ingest trees.
const uploadPinTTL = 30 * 24 * time.Hour

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

var (
	// ErrValidationFailed is returned when the create_app payload is invalid.
	ErrValidationFailed = errors.New("validation failed")
	// ErrPreviewConsumed is returned when a consumed preview cannot be replayed.
	ErrPreviewConsumed = errors.New("preview consumed")
	// ErrPreviewExpired is returned when the preview has expired.
	ErrPreviewExpired = errors.New("preview expired")
	// ErrPreviewNotFound is returned when the preview cannot be found.
	ErrPreviewNotFound = errors.New("preview not found")
	// ErrSlugTaken is returned when the slug is already in use.
	ErrSlugTaken = errors.New("slug taken")
)

// Store captures the db operations required by the create_app orchestration.
type Store interface {
	GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error
	CreateApp(ctx context.Context, params db.AppCreateParams) (db.AppCreateResult, error)
	DeleteAppByID(ctx context.Context, appID string) error
	PinUpload(ctx context.Context, teamID, id string, expiresAt time.Time) error
}

// K3sClient captures the namespace provisioning calls used by create_app.
// EnsureTeamIsolation must atomically provision the namespace together with
// ResourceQuota, LimitRange, NetworkPolicy and PSA labels; partial state is
// rolled back so create_app never observes a half-isolated team
// (spec § 15 hard rule #3).
type K3sClient interface {
	EnsureTeamIsolation(ctx context.Context, teamID, teamSlug, planTier string) (string, error)
}

// CloudflareClient captures the route provisioning calls used by create_app.
type CloudflareClient interface {
	RouteAppToDomain(ctx context.Context, teamID, teamSlug, appSlug string) (string, error)
	CreateTunnelRoute(ctx context.Context, teamID, appSlug, backendURL string) error
}

// Dispatcher triggers the GitHub build workflow.
type Dispatcher interface {
	Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error
}

// OpsTokenSigner issues ephemeral workflow tokens.
type OpsTokenSigner interface {
	Issue(runID, traceID string, scopes []string) (string, error)
}

// Service orchestrates create_app preview replay and confirmation.
type Service struct {
	store           Store
	k3sClient       K3sClient
	cfClient        CloudflareClient
	gitops          gitopssvc.Service
	dispatcher      Dispatcher
	tokenSigner     OpsTokenSigner
	callbackBaseURL string
	planTier        string
	now             func() time.Time

	// inspector resolves repo metadata for a Source / repoURL. nil is allowed
	// for backward compatibility with callers that don't yet have a router
	// wired (test fakes, the legacy redeploy path). T13/T14 consume this field.
	inspector Inspector

	// uploadTokenSigner mints the short-lived JWT that GHA's
	// deploy-app-from-upload workflow presents when calling
	// GET /v1/uploads/{id}/archive (T9). nil means skip token signing —
	// the fetch_token and fetch_url fields will be empty in the dispatch
	// payload. This is intentional for non-upload sources and for router
	// configurations without ingestion wired (e.g. dev with LocalDispatcher).
	uploadTokenSigner *ingestion.TokenSigner

	// uploadFetchBaseURL is the public origin used to build the archive
	// fetch_url sent to GHA. Empty falls back to callbackBaseURL at sign time.
	uploadFetchBaseURL string
}

// ConfirmResult is the durable create_app response plus replay metadata.
type ConfirmResult struct {
	Response         dto.AppCreateResponse
	Replayed         bool
	PreviewCreatedAt time.Time
}

// New returns a create_app orchestration service with no inspector.
// Use WithInspector to install one for upload/file source dispatch.
func New(store Store, k3sClient K3sClient, cfClient CloudflareClient, gitops gitopssvc.Service, dispatcher Dispatcher, tokenSigner OpsTokenSigner, callbackBaseURL string) *Service {
	return &Service{
		store:           store,
		k3sClient:       k3sClient,
		cfClient:        cfClient,
		gitops:          gitops,
		dispatcher:      dispatcher,
		tokenSigner:     tokenSigner,
		callbackBaseURL: strings.TrimRight(callbackBaseURL, "/"),
		planTier:        "free",
		now:             time.Now,
	}
}

// WithInspector installs the Inspector router used by T13/T14 to dispatch
// upload/file/github sources. Returns the receiver for chained construction.
// Passing nil is a no-op — it sets the field to nil, equivalent to never
// calling WithInspector. This matches the nil-tolerant pattern of the
// other Service dependencies (cfClient, k3sClient, dispatcher).
func (s *Service) WithInspector(insp Inspector) *Service {
	s.inspector = insp
	return s
}

// Inspector returns the Service's installed Inspector, or nil if not wired.
// Exposed for orchestration / dispatcher wire-up; callers should NOT call
// Inspect directly through this — go through the planned T13/T14 paths.
func (s *Service) Inspector() Inspector {
	return s.inspector
}

// WithUploadTokenSigner installs the T7 fetch-token signer used to mint
// the short-lived JWT that GHA's deploy-app-from-upload workflow presents
// when calling GET /v1/uploads/{id}/archive (T9). Passing nil is a no-op —
// the field is set to nil, equivalent to never calling WithUploadTokenSigner.
// fetchBaseURL is the public origin used to build the fetch URL; empty falls
// back to s.callbackBaseURL at sign time.
func (s *Service) WithUploadTokenSigner(signer *ingestion.TokenSigner, fetchBaseURL string) *Service {
	s.uploadTokenSigner = signer
	s.uploadFetchBaseURL = fetchBaseURL
	return s
}

// Confirm executes the create_app confirm flow for a validated preview.
func (s *Service) Confirm(ctx context.Context, teamID, actorUserID, teamSlug, previewID, traceID string) (ConfirmResult, error) {
	preview, err := s.store.GetPreview(ctx, previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			return ConfirmResult{}, ErrPreviewNotFound
		}
		return ConfirmResult{}, err
	}

	if preview.Action != previewAction || preview.TeamID != teamID || preview.ActorUserID != actorUserID {
		return ConfirmResult{}, ErrPreviewNotFound
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			return ConfirmResult{}, ErrPreviewConsumed
		}
		var response dto.AppCreateResponse
		if err := json.Unmarshal(preview.LastResult, &response); err != nil {
			return ConfirmResult{}, fmt.Errorf("decode replay result: %w", err)
		}
		return ConfirmResult{Response: response, Replayed: true, PreviewCreatedAt: preview.CreatedAt}, nil
	}
	if preview.ExpiresAt.Before(s.now().UTC()) {
		return ConfirmResult{}, ErrPreviewExpired
	}

	var payload dto.AppCreateRequest
	if err := json.Unmarshal(preview.Args, &payload); err != nil {
		return ConfirmResult{}, fmt.Errorf("decode preview args: %w", err)
	}
	if err := validateRequest(payload); err != nil {
		return ConfirmResult{}, fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}

	// M8: github sources carry the URL/ref in Source.GitHub. The build/deploy
	// pipeline below (DB persist, gitops render, workflow dispatch) consumes the
	// top-level repoURL/ref, so derive them from Source here. A Source-only
	// github request is the only github path post-M8; without this bridge it
	// would deploy against an empty repo URL and ref. Upload and dev file://
	// sources keep using the top-level fields unchanged.
	repoURL := payload.RepoURL
	ref := payload.Ref
	if payload.Source != nil && payload.Source.Type == dto.SourceKindGitHub && payload.Source.GitHub != nil {
		repoURL = payload.Source.GitHub.URL
		ref = payload.Source.GitHub.Ref
	}

	if _, err := s.store.GetTeamAppBySlug(ctx, teamID, payload.Slug); err == nil {
		return ConfirmResult{}, ErrSlugTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ConfirmResult{}, err
	}

	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = preview.ID
	}

	result, err := s.store.CreateApp(ctx, db.AppCreateParams{
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Slug:        payload.Slug,
		RepoURL:     repoURL,
		Ref:         ref,
		Builder:     payload.Builder,
		TraceID:     traceID,
	})
	if err != nil {
		return ConfirmResult{}, err
	}

	commitSHA := ref
	imageRef := fmt.Sprintf("ghcr.io/winshare/0ops-apps/%s/%s:%s", teamSlug, result.AppSlug, result.DeployRunID)
	subdomain := fmt.Sprintf("%s.winshare.tw", result.AppSlug)

	rollback := func(opErr error) error {
		if deleteErr := s.store.DeleteAppByID(ctx, result.AppID); deleteErr != nil {
			return errors.Join(opErr, fmt.Errorf("rollback app %s: %w", result.AppID, deleteErr))
		}
		return opErr
	}

	if s.cfClient != nil {
		routedDomain, err := s.cfClient.RouteAppToDomain(ctx, teamID, teamSlug, result.AppSlug)
		if err != nil {
			return ConfirmResult{}, rollback(err)
		}
		if routedDomain != "" {
			subdomain = routedDomain
		}
		if err := s.cfClient.CreateTunnelRoute(ctx, teamID, result.AppSlug, tunnelBackendURL); err != nil {
			return ConfirmResult{}, rollback(err)
		}
	}

	if s.k3sClient != nil {
		if _, err := s.k3sClient.EnsureTeamIsolation(ctx, teamID, teamSlug, s.planTier); err != nil {
			return ConfirmResult{}, rollback(err)
		}
	}

	if s.gitops != nil {
		gitopsResult, err := s.gitops.RenderAndPush(ctx, gitopssvc.RenderInput{
			Action:      previewAction,
			TeamSlug:    teamSlug,
			AppSlug:     result.AppSlug,
			RepoURL:     repoURL,
			Ref:         ref,
			DeployRunID: result.DeployRunID,
			PreviewID:   preview.ID,
			TraceID:     traceID,
			PrimaryPort: 3000,
		})
		if err != nil {
			return ConfirmResult{}, rollback(err)
		}
		if gitopsResult.SourceCommitSHA != "" {
			commitSHA = gitopsResult.SourceCommitSHA
		}
		if gitopsResult.ImageRef != "" {
			imageRef = gitopsResult.ImageRef
		}
	}

	if s.dispatcher != nil {
		if s.tokenSigner == nil {
			return ConfirmResult{}, rollback(errors.New("missing workflow token signer"))
		}
		opsToken := ""
		opsToken, err = s.tokenSigner.Issue(result.DeployRunID, traceID, []string{"ghcr:push", "callback:write"})
		if err != nil {
			return ConfirmResult{}, rollback(err)
		}

		// Determine source kind for the dispatcher (T14).
		var (
			sourceKind string
			uploadID   string
			fetchToken string
			fetchURL   string
		)
		if payload.Source != nil {
			sourceKind = string(payload.Source.Type) // "github" or "upload"
			if payload.Source.Type == dto.SourceKindUpload && payload.Source.Upload != nil {
				uploadID = payload.Source.Upload.UploadID
				if s.uploadTokenSigner != nil {
					tok, signErr := s.uploadTokenSigner.Sign(ingestion.TokenClaims{
						TeamID:      teamID,
						UploadID:    uploadID,
						DeployRunID: result.DeployRunID,
					})
					if signErr != nil {
						return ConfirmResult{}, rollback(fmt.Errorf("sign upload fetch token: %w", signErr))
					}
					fetchToken = tok
					base := s.uploadFetchBaseURL
					if base == "" {
						base = s.callbackBaseURL
					}
					fetchURL = fmt.Sprintf("%s/v1/uploads/%s/archive", strings.TrimRight(base, "/"), uploadID)
				}
			}
		} else if strings.HasPrefix(payload.RepoURL, "file://") {
			sourceKind = "local"
		}

		if err := s.dispatcher.Dispatch(ctx, workflowdispatch.ClientPayload{
			RunID:       result.DeployRunID,
			AppSlug:     result.AppSlug,
			TeamSlug:    teamSlug,
			CommitSHA:   commitSHA,
			Ref:         ref,
			ImageRef:    imageRef,
			OpsToken:    opsToken,
			CallbackURL: fmt.Sprintf("%s/internal/deploy-runs/%s/callback", s.callbackBaseURL, result.DeployRunID),
			TraceID:     traceID,
			SourceKind:  sourceKind,
			UploadID:    uploadID,
			FetchToken:  fetchToken,
			FetchURL:    fetchURL,
		}); err != nil {
			return ConfirmResult{}, rollback(err)
		}
	}

	// Pin the upload row if this create_app consumed an upload source. By this
	// point CreateApp + cloudflare + k3s + gitops + dispatcher have all
	// succeeded; rollback paths are exhausted. Pin failure must NOT roll back
	// because the deploy is in motion.
	//
	// Pin is placed BEFORE ConsumePreviewWithResult so that on retry (if the
	// consume step fails) the second Confirm call sees the upload already in
	// 'pinned' status and ErrUploadNotFound from PinUpload — caught above as
	// a benign skip rather than an error. This makes idempotent replay safe.
	if payload.Source != nil && payload.Source.Type == dto.SourceKindUpload && payload.Source.Upload != nil {
		uploadID := payload.Source.Upload.UploadID
		pinExpires := s.now().UTC().Add(uploadPinTTL)
		if err := s.store.PinUpload(ctx, teamID, uploadID, pinExpires); err != nil {
			if errors.Is(err, db.ErrUploadNotFound) {
				// Benign: row already pinned by an earlier Confirm attempt, or has
				// moved to a non-received status. T19 GC handles stale rows.
				slog.Warn("uploads: pin skipped; row not in 'received' status",
					"team", teamID, "upload", uploadID, "run_id", result.DeployRunID, "err", err)
			} else {
				slog.Error("uploads: pin failed; ingest tree may be GC'd before deploy completes",
					"team", teamID, "upload", uploadID, "run_id", result.DeployRunID, "err", err)
			}
		}
	}

	response := dto.AppCreateResponse{
		AppID:         result.AppID,
		AppSlug:       result.AppSlug,
		DeployRunID:   result.DeployRunID,
		TraceID:       traceID,
		SubdomainURL:  fmt.Sprintf("https://%s", subdomain),
		InitialDeploy: true,
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

	return ConfirmResult{Response: response, PreviewCreatedAt: preview.CreatedAt}, nil
}

func validateRequest(req dto.AppCreateRequest) error {
	slug := strings.TrimSpace(req.Slug)
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("invalid slug")
	}
	switch slug {
	case "system", "api", "auth", "v1", "me":
		return fmt.Errorf("reserved slug")
	}

	// Source-aware path: the HTTP-layer validator accepts github/upload only via
	// the Source sum type (M8 removed the github-via-repo_url alias) and rejects
	// file:// in production. The service layer trusts that pre-validation and only
	// does minimal kind sanity here (defense in depth, in case the preview row
	// was written by a bypassed code path).
	if req.Source != nil {
		switch req.Source.Type {
		case dto.SourceKindGitHub:
			if req.Source.GitHub == nil || strings.TrimSpace(req.Source.GitHub.URL) == "" || strings.TrimSpace(req.Source.GitHub.Ref) == "" {
				return fmt.Errorf("github source incomplete")
			}
		case dto.SourceKindUpload:
			if req.Source.Upload == nil || strings.TrimSpace(req.Source.Upload.UploadID) == "" {
				return fmt.Errorf("upload source incomplete")
			}
		default:
			return fmt.Errorf("source kind %q is unsupported", req.Source.Type)
		}
		return nil
	}

	// Legacy path: repo_url + ref. Kept ONLY for the dev file:// inspector flow
	// (the HTTP validator leaves req.Source nil for dev file:// requests). M8
	// removed the github-via-repo_url alias, so a non-file:// repo_url here is
	// unsupported.
	repoURL := strings.TrimSpace(req.RepoURL)
	if repoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if !strings.HasPrefix(repoURL, "file://") {
		return fmt.Errorf("unsupported repo_url")
	}
	if err := validateLocalRepoURL(repoURL); err != nil {
		return err
	}
	if strings.TrimSpace(req.Ref) == "" {
		return fmt.Errorf("ref is required")
	}
	return nil
}
