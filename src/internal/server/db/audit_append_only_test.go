package db_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

// TestAuditLogAppendOnlyRoleDeniesMutation is the slice-b2 integration test for
// the append-only DB role split (audit-export-and-integrity spec § 5.1, § 11 /
// ADR-0015 § 3.1; hard rules #1/#2). It connects as a login role that is a
// member of "0ops_app" — exactly the privilege envelope the production runtime
// connection runs under — and asserts the schema-level append-only invariant:
//
//   - SELECT on audit_log              → allowed
//   - UPDATE on audit_log              → permission denied (hard rule #1)
//   - DELETE on audit_log              → permission denied (hard rule #1)
//   - UPDATE on audit_chain_head       → allowed (the write path advances tip)
//
// The denial is enforced by Postgres grants, not application self-discipline,
// so a leaked runtime credential cannot rewrite history (threat-model AD1).
func TestAuditLogAppendOnlyRoleDeniesMutation(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	// A real chained row to attempt to tamper with.
	userID := seedUser(ctx, t, pool, "appendonly")
	teamID, _ := seedTeam(ctx, t, pool, "appendonly-team", "Append Only Team")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	if err := repo.InsertAuditLog(ctx, audit.InsertRow{
		TeamID: teamID, ActorUserID: &userID, Source: "user", SubjectType: "app",
		Action:  "create_app",
		Args:    json.RawMessage(`{"slug":"demo"}`),
		Result:  json.RawMessage(`{"ok":true}`),
		TraceID: "0af7651916cd43dd8448eb211c80319c",
		Outcome: "success",
	}); err != nil {
		t.Fatalf("seed audit row: %v", err)
	}

	appPool := connectAsAppRole(ctx, t, pool)

	// SELECT must work — the runtime reads audit_log for the query API.
	var got int64
	if err := appPool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE team_id = $1::uuid`, teamID).Scan(&got); err != nil {
		t.Fatalf("app role SELECT audit_log: %v", err)
	}
	if got == 0 {
		t.Fatalf("app role SELECT returned 0 rows, expected the seeded row")
	}

	// UPDATE must be denied — the core append-only guarantee.
	if _, err := appPool.Exec(ctx,
		`UPDATE audit_log SET action = 'tampered' WHERE team_id = $1::uuid`, teamID); err == nil {
		t.Fatalf("app role UPDATE audit_log succeeded; expected permission denied (hard rule #1)")
	} else if !isPermissionDenied(err) {
		t.Fatalf("app role UPDATE audit_log: want permission denied, got %v", err)
	}

	// DELETE must be denied.
	if _, err := appPool.Exec(ctx,
		`DELETE FROM audit_log WHERE team_id = $1::uuid`, teamID); err == nil {
		t.Fatalf("app role DELETE audit_log succeeded; expected permission denied (hard rule #1)")
	} else if !isPermissionDenied(err) {
		t.Fatalf("app role DELETE audit_log: want permission denied, got %v", err)
	}

	// UPDATE on audit_chain_head must remain allowed — the write path advances
	// tip_hash / row_count there on every INSERT (spec § 4.4, § 5.1).
	if _, err := appPool.Exec(ctx,
		`UPDATE audit_chain_head SET updated_at = now() WHERE team_id = $1::uuid`, teamID); err != nil {
		t.Fatalf("app role UPDATE audit_chain_head: want allowed, got %v", err)
	}
}

// TestCreateMonthlyPartitionRevokesAppMutation covers spec § 5.1 / § 8: a
// partition created by the rollover path must not be mutable by the runtime
// role, even though default privileges would otherwise grant UPDATE/DELETE on
// every new table. Without the per-partition REVOKE a leaked credential could
// rewrite history by addressing a fresh partition directly.
func TestCreateMonthlyPartitionRevokesAppMutation(t *testing.T) {
	repo, ctx, pool := newTestRepository(t)

	// A month far outside the 00007 seed window so the partition is genuinely new.
	month := time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC)
	partition := "audit_log_" + audit.PartitionLabel(month)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS `+quoteIdent(partition))
	})
	if err := repo.CreateMonthlyPartition(ctx, month); err != nil {
		t.Fatalf("CreateMonthlyPartition: %v", err)
	}

	appPool := connectAsAppRole(ctx, t, pool)

	if _, err := appPool.Exec(ctx,
		`UPDATE `+quoteIdent(partition)+` SET action = 'tampered'`); err == nil {
		t.Fatalf("app role UPDATE on new partition succeeded; expected permission denied (spec § 5.1)")
	} else if !isPermissionDenied(err) {
		t.Fatalf("app role UPDATE on new partition: want permission denied, got %v", err)
	}
	if _, err := appPool.Exec(ctx,
		`DELETE FROM `+quoteIdent(partition)); err == nil {
		t.Fatalf("app role DELETE on new partition succeeded; expected permission denied (spec § 5.1)")
	} else if !isPermissionDenied(err) {
		t.Fatalf("app role DELETE on new partition: want permission denied, got %v", err)
	}
}

// connectAsAppRole creates an ephemeral login role that is a member of
// "0ops_app" and returns a pool authenticated as it. The throwaway login role
// carries no privileges of its own, so its effective grants are exactly the
// inherited "0ops_app" envelope — the same one the production runtime uses.
func connectAsAppRole(ctx context.Context, t *testing.T, super *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()

	const pw = "appendonly_pw" //nolint:gosec // G101: ephemeral test-only role password
	roleName := strings.ToLower(uniqueSuffix(t, "test_app_login"))
	// Identifiers are derived from a controlled prefix, never user input.
	if _, err := super.Exec(ctx,
		`CREATE ROLE `+quoteIdent(roleName)+` LOGIN PASSWORD '`+pw+`' IN ROLE "0ops_app"`); err != nil {
		t.Fatalf("create app login role (is migration 00014 applied?): %v", err)
	}
	t.Cleanup(func() {
		_, _ = super.Exec(context.Background(), `DROP ROLE IF EXISTS `+quoteIdent(roleName))
	})

	appDSN := withCredentials(t, resolveTestDatabaseURL(), roleName, pw)
	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as app role: %v", err)
	}
	t.Cleanup(appPool.Close)
	return appPool
}

// withCredentials swaps the username / password of a libpq URL DSN.
func withCredentials(t *testing.T, dsn, user, pw string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	u.User = url.UserPassword(user, pw)
	return u.String()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func isPermissionDenied(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "permission denied")
}
