package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

// TestExportAuditLogYieldsVerifiableChain is the slice-c persistence test:
// ExportAuditLog returns the redacted, as-stored rows ascending by (created_at,
// id) with their prev_hash / row_hash, and ListChainHeads returns the anchor.
// The test recomputes the chain purely from the exported projection and matches
// it against the anchor tip — proving the export alone is offline-verifiable
// (spec § 6.4 closed loop; this is exactly what `0ops audit verify` does).
func TestExportAuditLogYieldsVerifiableChain(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "exporter")
	teamID, _ := seedTeam(ctx, t, pool, "export-team", "Export Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")

	inserts := []audit.InsertRow{
		{TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action: "create_app", Args: json.RawMessage(`{"slug":"demo"}`),
			Result: json.RawMessage(`{"ok":true}`), TraceID: "0af7651916cd43dd8448eb211c80319c", Outcome: "success"},
		{TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action: "redeploy", Args: json.RawMessage(`{"slug":"demo"}`),
			Result: json.RawMessage(`null`), TraceID: "0af7651916cd43dd8448eb211c80319c", Outcome: "success"},
		{TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action: "delete_app", Args: json.RawMessage(`{"slug":"demo"}`),
			Result: json.RawMessage(`{"deleted":true}`), TraceID: "0af7651916cd43dd8448eb211c80319c", Outcome: "success"},
	}
	for i, r := range inserts {
		if err := repo.InsertAuditLog(ctx, r); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	since := time.Now().UTC().AddDate(0, 0, -1)
	until := time.Now().UTC().Add(time.Hour)
	rows, err := repo.ExportAuditLog(ctx, audit.ExportFilter{
		TeamID: teamID, Since: since, Until: until, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ExportAuditLog: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("exported %d rows, want 3", len(rows))
	}
	// Ascending by id.
	for i := 1; i < len(rows); i++ {
		if rows[i].ID <= rows[i-1].ID {
			t.Fatalf("rows not ascending: %d then %d", rows[i-1].ID, rows[i].ID)
		}
	}

	heads, err := repo.ListChainHeads(ctx, teamID,
		audit.PartitionMonth(since), audit.PartitionMonth(until))
	if err != nil {
		t.Fatalf("ListChainHeads: %v", err)
	}
	if len(heads) != 1 {
		t.Fatalf("chain heads = %d, want 1", len(heads))
	}
	head := heads[0]
	if head.RowCount != 3 {
		t.Fatalf("head row_count = %d, want 3", head.RowCount)
	}

	// Recompute the chain from the export alone and match the anchor tip.
	prev := head.GenesisHash
	for i, r := range rows {
		if !bytes.Equal(r.PrevHash, prev) {
			t.Fatalf("row %d prev_hash %x != running %x", i, r.PrevHash, prev)
		}
		canon, err := audit.CanonicalCore(audit.Core{
			ID: r.ID, TeamID: r.TeamID, ActorUserID: r.ActorUserID, Source: r.Source,
			SubjectType: r.SubjectType, SubjectID: r.SubjectID, Action: r.Action,
			Args: r.Args, Result: r.Result, PreviewID: r.PreviewID, TraceID: r.TraceID,
			Outcome: r.Outcome, HTTPStatus: r.HTTPStatus, CreatedAt: r.CreatedAt,
		})
		if err != nil {
			t.Fatalf("row %d canonicalise: %v", i, err)
		}
		want := audit.RowHash(prev, canon)
		if !bytes.Equal(r.RowHash, want) {
			t.Fatalf("row %d row_hash %x != recomputed %x", i, r.RowHash, want)
		}
		prev = r.RowHash
	}
	if !bytes.Equal(prev, head.TipHash) {
		t.Fatalf("recomputed tip %x != head tip %x", prev, head.TipHash)
	}
}

// TestExportedChainDetectsTamper is the full-stack tamper-evidence test (spec
// § 11): export the real chain, verify it OK, then mutate a stored row with a
// privileged connection (the very threat the append-only split prevents for the
// runtime) and confirm verify now reports a break at that exact row.
func TestExportedChainDetectsTamper(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "tamper")
	teamID, _ := seedTeam(ctx, t, pool, "tamper-team", "Tamper Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	for i := 0; i < 3; i++ {
		if err := repo.InsertAuditLog(ctx, audit.InsertRow{
			TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action: "create_app", Args: json.RawMessage(`{"n":` + strconv.Itoa(i) + `}`),
			Result: json.RawMessage(`null`), TraceID: "0af7651916cd43dd8448eb211c80319c", Outcome: "success",
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	since := time.Now().UTC().AddDate(0, 0, -1)
	until := time.Now().UTC().Add(time.Hour)

	if v := verifyExportedChain(ctx, t, repo, teamID, since, until); !v.OK {
		t.Fatalf("clean chain should verify OK, got break at id=%d: %s", v.BreakID, v.BreakReason)
	}

	// Tamper the middle row's stored args via the privileged (ops) connection.
	rows, err := repo.ExportAuditLog(ctx, audit.ExportFilter{TeamID: teamID, Since: since, Until: until, Limit: 100})
	if err != nil {
		t.Fatalf("export for tamper id: %v", err)
	}
	target := rows[1].ID
	if _, err := pool.Exec(ctx,
		`UPDATE audit_log SET args = '{"tampered":true}'::jsonb WHERE id = $1`, target); err != nil {
		t.Fatalf("tamper update: %v", err)
	}

	v := verifyExportedChain(ctx, t, repo, teamID, since, until)
	if v.OK {
		t.Fatal("verify should detect the tampered row")
	}
	if v.BreakID != target {
		t.Fatalf("break id = %d, want tampered id %d", v.BreakID, target)
	}
}

// verifyExportedChain re-derives the single chain from the export + anchor,
// mirroring exactly what `0ops audit verify` does offline.
func verifyExportedChain(ctx context.Context, t *testing.T, repo *dbpkg.Repository, teamID string, since, until time.Time) audit.ChainVerdict {
	t.Helper()
	rows, err := repo.ExportAuditLog(ctx, audit.ExportFilter{TeamID: teamID, Since: since, Until: until, Limit: 1000})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	heads, err := repo.ListChainHeads(ctx, teamID, audit.PartitionMonth(since), audit.PartitionMonth(until))
	if err != nil {
		t.Fatalf("heads: %v", err)
	}
	if len(heads) != 1 {
		t.Fatalf("expected 1 chain head, got %d", len(heads))
	}
	vrows := make([]audit.VerifyRow, 0, len(rows))
	for _, r := range rows {
		vrows = append(vrows, audit.VerifyRow{
			Core: audit.Core{
				ID: r.ID, TeamID: r.TeamID, ActorUserID: r.ActorUserID, Source: r.Source,
				SubjectType: r.SubjectType, SubjectID: r.SubjectID, Action: r.Action,
				Args: r.Args, Result: r.Result, PreviewID: r.PreviewID, TraceID: r.TraceID,
				Outcome: r.Outcome, HTTPStatus: r.HTTPStatus, CreatedAt: r.CreatedAt,
			},
			PrevHash: r.PrevHash,
			RowHash:  r.RowHash,
		})
	}
	return audit.VerifyChain(audit.VerifyChainInput{
		TeamID: teamID, Month: heads[0].PartitionMonth, Genesis: heads[0].GenesisHash,
		Tip: heads[0].TipHash, RowCount: heads[0].RowCount, Rows: vrows,
	})
}

// TestExportAuditLogCursorPaginates checks the keyset cursor walks the range
// forward without gaps or overlaps.
func TestExportAuditLogCursorPaginates(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "exporter-cursor")
	teamID, _ := seedTeam(ctx, t, pool, "export-cursor-team", "Export Cursor Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	for i := 0; i < 3; i++ {
		if err := repo.InsertAuditLog(ctx, audit.InsertRow{
			TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action: "create_app", Args: json.RawMessage(`{}`), Result: json.RawMessage(`null`),
			TraceID: "0af7651916cd43dd8448eb211c80319c", Outcome: "success",
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	since := time.Now().UTC().AddDate(0, 0, -1)
	until := time.Now().UTC().Add(time.Hour)

	page1, err := repo.ExportAuditLog(ctx, audit.ExportFilter{TeamID: teamID, Since: since, Until: until, Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 = %d rows, want 2", len(page1))
	}
	cursor := audit.EncodeCursor(page1[1].CreatedAt, page1[1].ID)
	page2, err := repo.ExportAuditLog(ctx, audit.ExportFilter{TeamID: teamID, Since: since, Until: until, Cursor: cursor, Limit: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 = %d rows, want 1", len(page2))
	}
	if page2[0].ID <= page1[1].ID {
		t.Fatalf("page2 row id %d did not advance past cursor id %d", page2[0].ID, page1[1].ID)
	}
}
