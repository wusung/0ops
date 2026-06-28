package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

// TestInsertAuditLogChainRecomputable is the write-path integration test for
// the hash chain (M9.1 slice b): insert several rows, then recompute the whole
// (team, month) chain from what was stored and assert it matches — a mini
// verify proving the write path emits a chain a third party can validate.
func TestInsertAuditLogChainRecomputable(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	userID := seedUser(ctx, t, pool, "alice")
	teamID, _ := seedTeam(ctx, t, pool, "chain-team", "Chain Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")

	rows := []audit.InsertRow{
		{
			TeamID: teamID, Source: "system", SubjectType: "system",
			Action:  "reconciler_failed_permanently",
			Args:    json.RawMessage(`{"job":"cleanup","attempt":8}`),
			Result:  json.RawMessage(`null`),
			TraceID: "00000000000000000000000000000001",
			Outcome: "failure",
		},
		{
			TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action:  "create_app",
			Args:    json.RawMessage(`{"slug":"demo","n":200}`),
			Result:  json.RawMessage(`{"ok":true}`),
			TraceID: "0af7651916cd43dd8448eb211c80319c",
			Outcome: "success",
		},
		{
			TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
			Action:  "delete_app",
			Args:    json.RawMessage(`{"slug":"demo"}`),
			Result:  json.RawMessage(`{"deleted":true}`),
			TraceID: "0af7651916cd43dd8448eb211c80319c",
			Outcome: "success",
		},
	}
	for i, r := range rows {
		if err := repo.InsertAuditLog(ctx, r); err != nil {
			t.Fatalf("InsertAuditLog row %d: %v", i, err)
		}
	}

	// Read the chain head.
	var genesis, tip []byte
	var rowCount int64
	if err := pool.QueryRow(ctx, `
SELECT genesis_hash, tip_hash, row_count
FROM audit_chain_head WHERE team_id = $1::uuid
`, teamID).Scan(&genesis, &tip, &rowCount); err != nil {
		t.Fatalf("read chain head: %v", err)
	}
	if rowCount != int64(len(rows)) {
		t.Fatalf("row_count = %d, want %d", rowCount, len(rows))
	}

	month := audit.PartitionMonth(time.Now().UTC())
	if wantGenesis := audit.GenesisHash(teamID, month); !bytes.Equal(genesis, wantGenesis) {
		t.Fatalf("head genesis mismatch:\n got  %x\n want %x", genesis, wantGenesis)
	}

	// Recompute the chain from stored rows.
	stored := readChainRows(ctx, t, pool, teamID)
	if len(stored) != len(rows) {
		t.Fatalf("read %d rows, want %d", len(stored), len(rows))
	}
	prev := genesis
	for i, sr := range stored {
		if !bytes.Equal(sr.prevHash, prev) {
			t.Errorf("row %d (id=%d): stored prev_hash %x != running prev %x", i, sr.id, sr.prevHash, prev)
		}
		canon, err := audit.CanonicalCore(sr.core)
		if err != nil {
			t.Fatalf("row %d canonicalise: %v", i, err)
		}
		want := audit.RowHash(prev, canon)
		if !bytes.Equal(sr.rowHash, want) {
			t.Errorf("row %d (id=%d): stored row_hash %x != recomputed %x", i, sr.id, sr.rowHash, want)
		}
		prev = sr.rowHash
	}
	if !bytes.Equal(prev, tip) {
		t.Fatalf("head tip %x != last row_hash %x", tip, prev)
	}
}

// TestInsertAuditLogChainsAreTeamIsolated proves two teams keep independent
// chains: each gets its own genesis and its own head.
func TestInsertAuditLogChainsAreTeamIsolated(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	teamA, _ := seedTeam(ctx, t, pool, "iso-team-a", "Iso A")
	teamB, _ := seedTeam(ctx, t, pool, "iso-team-b", "Iso B")

	mk := func(team string) audit.InsertRow {
		return audit.InsertRow{
			TeamID: team, Source: "system", SubjectType: "system",
			Action: "login", Args: json.RawMessage(`{}`), Result: json.RawMessage(`null`),
			TraceID: "00000000000000000000000000000001", Outcome: "success",
		}
	}
	if err := repo.InsertAuditLog(ctx, mk(teamA)); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := repo.InsertAuditLog(ctx, mk(teamB)); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	month := audit.PartitionMonth(time.Now().UTC())
	gA := readGenesis(ctx, t, pool, teamA)
	gB := readGenesis(ctx, t, pool, teamB)
	if !bytes.Equal(gA, audit.GenesisHash(teamA, month)) {
		t.Error("team A genesis mismatch")
	}
	if bytes.Equal(gA, gB) {
		t.Error("distinct teams must have distinct genesis")
	}
}

type storedRow struct {
	id       int64
	core     audit.Core
	prevHash []byte
	rowHash  []byte
}

func readChainRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID string) []storedRow {
	t.Helper()
	pgRows, err := pool.Query(ctx, `
SELECT id, team_id::text, actor_user_id::text, source,
       COALESCE(subject_type, ''), subject_id::text, action,
       COALESCE(args, 'null'::jsonb), COALESCE(result, 'null'::jsonb),
       preview_id::text, COALESCE(trace_id, ''), outcome, http_status,
       created_at, prev_hash, row_hash
FROM audit_log WHERE team_id = $1::uuid
ORDER BY id ASC
`, teamID)
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer pgRows.Close()

	var out []storedRow
	for pgRows.Next() {
		var (
			sr                                 storedRow
			team                               string
			actor, subjectID, preview          *string
			subjectType, action, source, trace string
			outcome                            string
			args, result                       []byte
			httpStatus                         *int
			createdAt                          time.Time
		)
		if err := pgRows.Scan(&sr.id, &team, &actor, &source, &subjectType, &subjectID,
			&action, &args, &result, &preview, &trace, &outcome, &httpStatus,
			&createdAt, &sr.prevHash, &sr.rowHash); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		sr.core = audit.Core{
			ID: sr.id, TeamID: team, ActorUserID: actor, Source: source,
			SubjectType: subjectType, SubjectID: subjectID, Action: action,
			Args: json.RawMessage(args), Result: json.RawMessage(result),
			PreviewID: preview, TraceID: trace, Outcome: outcome,
			HTTPStatus: httpStatus, CreatedAt: createdAt,
		}
		out = append(out, sr)
	}
	if err := pgRows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return out
}

func readGenesis(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID string) []byte {
	t.Helper()
	var g []byte
	if err := pool.QueryRow(ctx,
		`SELECT genesis_hash FROM audit_chain_head WHERE team_id = $1::uuid`, teamID).Scan(&g); err != nil {
		t.Fatalf("read genesis: %v", err)
	}
	return g
}
