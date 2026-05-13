package createapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	gitopssvc "github.com/winshare/zeroops/internal/server/services/gitops"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
	"github.com/winshare/zeroops/internal/shared/dto"
)

const previewAction = "create_app"

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
}

// K3sClient captures the namespace provisioning calls used by create_app.
type K3sClient interface {
	EnsureNamespace(ctx context.Context, teamID, teamSlug, planTier string) (string, error)
}

// CloudflareClient captures the route provisioning calls used by create_app.
type CloudflareClient interface {
	RouteAppToDomain(ctx context.Context, teamID, teamSlug, appSlug string) (string, error)
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
}

// ConfirmResult is the durable create_app response plus replay metadata.
type ConfirmResult struct {
	Response         dto.AppCreateResponse
	Replayed         bool
	PreviewCreatedAt time.Time
}

// New returns a create_app orchestration service.
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
		RepoURL:     payload.RepoURL,
		Ref:         payload.Ref,
		Builder:     payload.Builder,
		TraceID:     traceID,
	})
	if err != nil {
		return ConfirmResult{}, err
	}

	commitSHA := payload.Ref
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
	}

	if s.k3sClient != nil {
		if _, err := s.k3sClient.EnsureNamespace(ctx, teamID, teamSlug, s.planTier); err != nil {
			return ConfirmResult{}, rollback(err)
		}
	}

	if s.gitops != nil {
		gitopsResult, err := s.gitops.RenderAndPush(ctx, gitopssvc.RenderInput{
			Action:      previewAction,
			TeamSlug:    teamSlug,
			AppSlug:     result.AppSlug,
			RepoURL:     payload.RepoURL,
			Ref:         payload.Ref,
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
		if err := s.dispatcher.Dispatch(ctx, workflowdispatch.ClientPayload{
			RunID:       result.DeployRunID,
			AppSlug:     result.AppSlug,
			TeamSlug:    teamSlug,
			CommitSHA:   commitSHA,
			Ref:         payload.Ref,
			ImageRef:    imageRef,
			OpsToken:    opsToken,
			CallbackURL: fmt.Sprintf("%s/internal/deploy-runs/%s/callback", s.callbackBaseURL, result.DeployRunID),
			TraceID:     traceID,
		}); err != nil {
			return ConfirmResult{}, rollback(err)
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

	repoURL := strings.TrimSpace(req.RepoURL)
	if repoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if !strings.HasPrefix(repoURL, "https://github.com/") && !strings.HasPrefix(repoURL, "git@github.com:") {
		return fmt.Errorf("unsupported repo_url")
	}
	if strings.TrimSpace(req.Ref) == "" {
		return fmt.Errorf("ref is required")
	}
	return nil
}
