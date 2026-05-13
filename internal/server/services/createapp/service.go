package createapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"

	"github.com/jackc/pgx/v5"
)

const previewAction = "create_app"

type Store interface {
	ResolveTeamBySlug(context.Context, string) (db.Team, error)
	GetTeamAppBySlug(context.Context, string, string) (db.App, error)
	CreatePreview(context.Context, string, string, string, json.RawMessage, string) (db.Preview, error)
	GetPreview(context.Context, string) (db.Preview, error)
	ConsumePreviewWithResult(context.Context, string, json.RawMessage) error
	CreateApp(context.Context, db.AppCreateParams) (db.AppCreateResult, error)
	DeleteAppByID(context.Context, string) error
}

type K3sClient interface {
	EnsureNamespace(context.Context, string, string, string) (string, error)
	EnsureResourceQuota(context.Context, string, string) error
	EnsureLimitRange(context.Context, string) error
	EnsureNetworkPolicy(context.Context, string) error
	PatchNamespacePSA(context.Context, string) error
}

type Service struct {
	store Store
	k3s   K3sClient
	now   func() time.Time
}

func New(store Store, k3s K3sClient) *Service {
	return &Service{
		store: store,
		k3s:   k3s,
		now:   time.Now,
	}
}

func (s *Service) PreviewCreateApp(ctx context.Context, teamSlug, actorUserID string, req dto.AppCreateRequest) (db.Preview, string, error) {
	if err := validateSlug(req.Slug); err != nil {
		return db.Preview{}, "", apperror.New(apperror.ClassBadRequest, "validation_failed", "invalid app slug", map[string]any{"field": "slug"})
	}
	if err := validateRepoURL(req.RepoURL); err != nil {
		return db.Preview{}, "", apperror.New(apperror.ClassBadRequest, "validation_failed", "invalid repo url", map[string]any{"field": "repo_url"})
	}
	if strings.TrimSpace(req.Ref) == "" {
		return db.Preview{}, "", apperror.New(apperror.ClassBadRequest, "validation_failed", "ref is required", map[string]any{"field": "ref"})
	}

	team, err := s.store.ResolveTeamBySlug(ctx, teamSlug)
	if err != nil {
		return db.Preview{}, "", apperror.New(apperror.ClassNotFound, "team_not_found", "team not found", nil)
	}
	if team.ArchivedAt != nil {
		return db.Preview{}, "", apperror.New(apperror.ClassNotFound, "team_not_found", "team not found", nil)
	}
	if _, err := s.store.GetTeamAppBySlug(ctx, team.ID, req.Slug); err == nil {
		return db.Preview{}, "", apperror.New(apperror.ClassConflict, "slug_taken", "app slug already exists", map[string]any{"slug": req.Slug})
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Preview{}, "", apperror.New(apperror.ClassInternal, "internal_error", "failed to check app slug", nil)
	}

	args, err := json.Marshal(req)
	if err != nil {
		return db.Preview{}, "", apperror.New(apperror.ClassInternal, "internal_error", "failed to encode preview args", nil)
	}

	summary := fmt.Sprintf("Create app %q from %s", req.Slug, req.RepoURL)
	preview, err := s.store.CreatePreview(ctx, team.ID, actorUserID, previewAction, args, summary)
	if err != nil {
		return db.Preview{}, "", apperror.New(apperror.ClassInternal, "internal_error", "failed to create preview", nil)
	}
	return preview, summary, nil
}

func (s *Service) ConfirmCreateApp(ctx context.Context, teamSlug, actorUserID, previewID, traceID string) (dto.AppCreateResponse, bool, error) {
	if strings.TrimSpace(previewID) == "" {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassBadRequest, "validation_failed", "preview_id is required", nil)
	}

	team, err := s.store.ResolveTeamBySlug(ctx, teamSlug)
	if err != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassNotFound, "team_not_found", "team not found", nil)
	}
	if team.ArchivedAt != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassNotFound, "team_not_found", "team not found", nil)
	}
	preview, err := s.store.GetPreview(ctx, previewID)
	if err != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassNotFound, "preview_not_found", "preview not found", nil)
	}
	if preview.Action != previewAction || preview.TeamID != team.ID || preview.ActorUserID != actorUserID {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassNotFound, "preview_not_found", "preview not found", nil)
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassConflict, "preview_consumed", "preview already consumed", nil)
		}
		var replay dto.AppCreateResponse
		if err := json.Unmarshal(preview.LastResult, &replay); err != nil {
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "internal_error", "failed to decode replay result", nil)
		}
		return replay, true, nil
	}
	if preview.ExpiresAt.Before(s.now().UTC()) {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassConflict, "preview_expired", "preview expired", nil)
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = preview.ID
	}

	var req dto.AppCreateRequest
	if err := json.Unmarshal(preview.Args, &req); err != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassBadRequest, "validation_failed", "invalid preview args", nil)
	}
	if err := validateSlug(req.Slug); err != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassBadRequest, "validation_failed", "invalid app slug", map[string]any{"field": "slug"})
	}
	if err := validateRepoURL(req.RepoURL); err != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassBadRequest, "validation_failed", "invalid repo url", map[string]any{"field": "repo_url"})
	}
	if strings.TrimSpace(req.Ref) == "" {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassBadRequest, "validation_failed", "ref is required", map[string]any{"field": "ref"})
	}
	if _, err := s.store.GetTeamAppBySlug(ctx, team.ID, req.Slug); err == nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassConflict, "slug_taken", "app slug already exists", map[string]any{"slug": req.Slug})
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "internal_error", "failed to check app slug", nil)
	}

	result, err := s.store.CreateApp(ctx, db.AppCreateParams{
		TeamID:      team.ID,
		ActorUserID: actorUserID,
		Slug:        req.Slug,
		RepoURL:     req.RepoURL,
		Ref:         req.Ref,
		Builder:     req.Builder,
		TraceID:     traceID,
	})
	if err != nil {
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "internal_error", "failed to create app", nil)
	}

	namespace := ""
	if s.k3s != nil {
		namespace, err = s.k3s.EnsureNamespace(ctx, team.ID, team.Slug, team.Plan)
		if err != nil {
			_ = s.store.DeleteAppByID(ctx, result.AppID)
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "k3s_namespace_failed", "failed to ensure team namespace", nil)
		}
		if err := s.k3s.EnsureResourceQuota(ctx, namespace, team.Plan); err != nil {
			_ = s.store.DeleteAppByID(ctx, result.AppID)
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "k3s_resource_quota_failed", "failed to ensure namespace quota", nil)
		}
		if err := s.k3s.EnsureLimitRange(ctx, namespace); err != nil {
			_ = s.store.DeleteAppByID(ctx, result.AppID)
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "k3s_limit_range_failed", "failed to ensure namespace limit range", nil)
		}
		if err := s.k3s.EnsureNetworkPolicy(ctx, namespace); err != nil {
			_ = s.store.DeleteAppByID(ctx, result.AppID)
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "k3s_network_policy_failed", "failed to ensure namespace network policy", nil)
		}
		if err := s.k3s.PatchNamespacePSA(ctx, namespace); err != nil {
			_ = s.store.DeleteAppByID(ctx, result.AppID)
			return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "k3s_psa_failed", "failed to patch namespace psa", nil)
		}
	}

	response := dto.AppCreateResponse{
		AppID:         result.AppID,
		AppSlug:       result.AppSlug,
		DeployRunID:   result.DeployRunID,
		TraceID:       traceID,
		SubdomainURL:  fmt.Sprintf("https://%s.winshare.tw", result.AppSlug),
		InitialDeploy: true,
	}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		_ = s.store.DeleteAppByID(ctx, result.AppID)
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassInternal, "internal_error", "failed to encode create_app result", nil)
	}
	if err := s.store.ConsumePreviewWithResult(ctx, preview.ID, responseJSON); err != nil {
		_ = s.store.DeleteAppByID(ctx, result.AppID)
		return dto.AppCreateResponse{}, false, apperror.New(apperror.ClassConflict, "preview_consumed", "preview already consumed", nil)
	}

	return response, false, nil
}
