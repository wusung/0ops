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

