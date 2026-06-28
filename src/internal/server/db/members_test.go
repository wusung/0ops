package db_test

import (
	"encoding/json"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

func TestCreatePreviewPersistsTraceIDFromContext(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "cp-trace-team", "CP Trace Team")
	actorID := seedUser(ctx, t, pool, "cp-trace-user")
	seedMembership(ctx, t, pool, teamID, actorID, "owner")

	const traceID = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
	ctxWithTrace := audit.WithTraceID(ctx, traceID)

	pv, err := repo.CreatePreview(ctxWithTrace, teamID, actorID, "app.create", json.RawMessage(`{}`), "")
	if err != nil {
		t.Fatalf("CreatePreview: %v", err)
	}
	if pv.TraceID != traceID {
		t.Fatalf("returned Preview.TraceID = %q, want %q", pv.TraceID, traceID)
	}

	got, err := repo.GetPreview(ctx, pv.ID)
	if err != nil {
		t.Fatalf("GetPreview: %v", err)
	}
	if got.TraceID != traceID {
		t.Fatalf("loaded Preview.TraceID = %q, want %q", got.TraceID, traceID)
	}
}

func TestCreatePreviewComputesRiskLevelAndPhrase(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "risk-team", "Risk Team")
	actorID := seedUser(ctx, t, pool, "risk-user")
	seedMembership(ctx, t, pool, teamID, actorID, "owner")

	t.Run("delete_app preview stores critical + DELETE phrase", func(t *testing.T) {
		args := json.RawMessage(`{"confirm":"billing-api"}`)
		pv, err := repo.CreatePreview(ctx, teamID, actorID, "delete_app", args, "")
		if err != nil {
			t.Fatalf("CreatePreview: %v", err)
		}
		if pv.RiskLevel != "critical" {
			t.Fatalf("returned RiskLevel = %q, want critical", pv.RiskLevel)
		}
		if pv.RequiredPhrase != "DELETE billing-api" {
			t.Fatalf("returned RequiredPhrase = %q, want %q", pv.RequiredPhrase, "DELETE billing-api")
		}
		got, err := repo.GetPreview(ctx, pv.ID)
		if err != nil {
			t.Fatalf("GetPreview: %v", err)
		}
		if got.RiskLevel != "critical" || got.RequiredPhrase != "DELETE billing-api" {
			t.Fatalf("loaded risk=%q phrase=%q, want critical / DELETE billing-api", got.RiskLevel, got.RequiredPhrase)
		}
	})

	t.Run("create_app preview is normal with empty phrase", func(t *testing.T) {
		pv, err := repo.CreatePreview(ctx, teamID, actorID, "create_app", json.RawMessage(`{"slug":"x"}`), "")
		if err != nil {
			t.Fatalf("CreatePreview: %v", err)
		}
		got, err := repo.GetPreview(ctx, pv.ID)
		if err != nil {
			t.Fatalf("GetPreview: %v", err)
		}
		if got.RiskLevel != "normal" {
			t.Fatalf("loaded RiskLevel = %q, want normal", got.RiskLevel)
		}
		if got.RequiredPhrase != "" {
			t.Fatalf("loaded RequiredPhrase = %q, want empty", got.RequiredPhrase)
		}
	})
}

func TestCreatePreviewMissingTraceIDFallsBackToSentinel(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)
	teamID, _ := seedTeam(ctx, t, pool, "cp-sentinel-team", "CP Sentinel Team")
	actorID := seedUser(ctx, t, pool, "cp-sentinel-user")
	seedMembership(ctx, t, pool, teamID, actorID, "owner")

	pv, err := repo.CreatePreview(ctx, teamID, actorID, "app.create", json.RawMessage(`{}`), "")
	if err != nil {
		t.Fatalf("CreatePreview: %v", err)
	}
	const sentinel = "00000000000000000000000000000000"
	if pv.TraceID != sentinel {
		t.Fatalf("Preview.TraceID = %q, want sentinel %q", pv.TraceID, sentinel)
	}

	got, err := repo.GetPreview(ctx, pv.ID)
	if err != nil {
		t.Fatalf("GetPreview: %v", err)
	}
	if got.TraceID != sentinel {
		t.Fatalf("loaded Preview.TraceID = %q, want sentinel %q", got.TraceID, sentinel)
	}
}
