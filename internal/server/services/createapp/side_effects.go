package createapp

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// Five side effects per spec § 5.2
// R1-R3: reversible, I1-I2: irreversible

type SideEffect interface {
	Name() string
	Description() string
}

type DBInsertSideEffect struct {
	AppID string
}

func (s *DBInsertSideEffect) Name() string        { return "db_insert" }
func (s *DBInsertSideEffect) Description() string { return fmt.Sprintf("Insert app %s into database", s.AppID) }

type K3sNamespaceSideEffect struct {
	AppID string
	Slug  string
}

func (s *K3sNamespaceSideEffect) Name() string        { return "k3s_namespace" }
func (s *K3sNamespaceSideEffect) Description() string { return fmt.Sprintf("Create K3s namespace %s", s.Slug) }

type GitOpsRenderPushSideEffect struct {
	AppID string
	Slug  string
	Ref   string
}

func (s *GitOpsRenderPushSideEffect) Name() string        { return "gitops_render_push" }
func (s *GitOpsRenderPushSideEffect) Description() string { return fmt.Sprintf("Render and push GitOps config for %s@%s", s.Slug, s.Ref) }

type OpsTokenSignSideEffect struct {
	AppID string
}

func (s *OpsTokenSignSideEffect) Name() string        { return "ops_token_sign" }
func (s *OpsTokenSignSideEffect) Description() string { return fmt.Sprintf("Sign ops_token for app %s", s.AppID) }

type GHADispatchSideEffect struct {
	AppID string
	Ref   string
}

func (s *GHADispatchSideEffect) Name() string        { return "gha_dispatch" }
func (s *GHADispatchSideEffect) Description() string { return fmt.Sprintf("Dispatch GHA workflow for %s@%s", s.AppID, s.Ref) }
func (s *Service) SideEffects(ctx context.Context, args *AppCreateArgs) ([]SideEffect, error) {
	// Spec § 5.2: compute 5 side effects
	// R1: DB INSERT app, domain_binding, deploy_run
	// R2: K3s namespace create
	// R3: GitOps render + push
	// I1: ops_token sign
	// I2: GHA workflow dispatch

	appID := generateAppID()

	effects := []SideEffect{
		&DBInsertSideEffect{AppID: appID},
		&K3sNamespaceSideEffect{AppID: appID, Slug: args.Slug},
		&GitOpsRenderPushSideEffect{AppID: appID, Slug: args.Slug, Ref: args.Ref},
		&OpsTokenSignSideEffect{AppID: appID},
		&GHADispatchSideEffect{AppID: appID, Ref: args.Ref},
	}

	return effects, nil
}

func generateAppID() string {
	// Per spec § ADR-0002: deterministic IDs with slug prefix
	// For now, use timestamp + random suffix
	ts := time.Now().UnixNano() / 1e6 // milliseconds
	rand.Seed(ts)
	suffix := rand.Intn(10000)
	return fmt.Sprintf("app_%d_%04d", ts, suffix)
}

// Precheck validates preconditions
func (s *Service) Precheck(ctx context.Context, args *AppCreateArgs) error {
	// 1. Validate args format
	if err := args.Validate(); err != nil {
		return fmt.Errorf("args validation failed: %w", err)
	}

	// 2. Check for conflicts (placeholder - would use DB)
	// Per spec § 5.3 step 2: app_id_suggest collision check
	// Actual implementation requires DB queries

	// 3. Verify external service availability (placeholder)
	// Per spec § 5.3 step 3: k3s, gitops, gha api checks

	// 4. Check domain ownership (placeholder)
	// Per spec § 5.3 step 4: domain_verify logic

	return nil
}

