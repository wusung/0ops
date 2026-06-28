package notify

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

// auditRepo wires a db.Repository with the notify enqueuer installed.
func auditRepoWithEnqueuer(t *testing.T, pool *pgxpool.Pool, obs MetricObserver) *audit.Service {
	t.Helper()
	repo := newRepoWithEnqueuer(t, pool, obs)
	return audit.NewService(repo, repo, nil)
}

func logDeleteApp(ctx context.Context, t *testing.T, svc *audit.Service, teamID string, actorID *string, action string) {
	t.Helper()
	subj := "22222222-2222-2222-2222-222222222222"
	err := svc.Log(audit.WithTraceID(ctx, "11111111111111111111111111111111"), audit.Entry{
		TeamID:      teamID,
		ActorUserID: actorID,
		Source:      audit.SourceUser,
		SubjectType: "app",
		SubjectID:   &subj,
		Action:      action,
		Outcome:     audit.OutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("audit Log(%s): %v", action, err)
	}
}

func TestEnqueueCreatesDeliveryForSubscribedEvent(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "alice")
	teamID, _ := seedTeam(ctx, t, pool, "acme", "Acme")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})

	svc := auditRepoWithEnqueuer(t, pool, nil)
	logDeleteApp(ctx, t, svc, teamID, &userID, "delete_app_confirm")

	if got := countDeliveries(ctx, t, pool, subID); got != 1 {
		t.Fatalf("delivery count = %d, want 1", got)
	}

	// The stored payload is redacted + carries the resolved slug/actor + audit id.
	var payload []byte
	var auditLogID int64
	var event string
	if err := pool.QueryRow(ctx, `SELECT payload, audit_log_id, event FROM webhook_delivery WHERE subscription_id = $1`, subID).
		Scan(&payload, &auditLogID, &event); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if event != "app.deleted" {
		t.Errorf("event = %q, want app.deleted", event)
	}
	if !AssertNoForbiddenKeys(payload) {
		t.Errorf("stored payload tripped forbidden-key guard: %s", payload)
	}
	var decoded map[string]any
	_ = json.Unmarshal(payload, &decoded)
	if decoded["team_slug"] == "" || decoded["team_slug"] == nil {
		t.Errorf("payload missing team_slug: %s", payload)
	}
	if int64(decoded["audit_id"].(float64)) != auditLogID {
		t.Errorf("payload audit_id != row audit_log_id")
	}
}

func TestEnqueueSkipsUnsubscribedEvent(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "bob")
	teamID, _ := seedTeam(ctx, t, pool, "beta", "Beta")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})

	svc := auditRepoWithEnqueuer(t, pool, nil)
	// token_revoke is catalogued but NOT subscribed by this subscription.
	logDeleteApp(ctx, t, svc, teamID, &userID, "token_revoke")
	// member_invite is subscribable but also not in the events subset.
	logDeleteApp(ctx, t, svc, teamID, &userID, "member_invite")

	if got := countDeliveries(ctx, t, pool, subID); got != 0 {
		t.Fatalf("delivery count = %d, want 0 (event not subscribed)", got)
	}
}

func TestEnqueueSkipsNonCatalogAction(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "carol")
	teamID, _ := seedTeam(ctx, t, pool, "gamma", "Gamma")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})

	svc := auditRepoWithEnqueuer(t, pool, nil)
	logDeleteApp(ctx, t, svc, teamID, &userID, "delete_app_preview") // not subscribable

	if got := countDeliveries(ctx, t, pool, subID); got != 0 {
		t.Fatalf("delivery count = %d, want 0 (non-catalog action)", got)
	}
}

func TestInsertDeliveryDedupIsIdempotent(t *testing.T) {
	pool, ctx := newTestPool(t)
	teamID, _ := seedTeam(ctx, t, pool, "dedup", "Dedup")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ev := dbpkg.AuditEnqueueEvent{AuditLogID: 7777, TeamID: teamID, OccurredAt: timeFixed()}
	body := []byte(`{"event":"app.deleted"}`)
	// Same (subscription, audit_log_id, created_at) twice: the second insert hits
	// the dedup unique index (23505) and is swallowed as a no-op (hard rule #7).
	if err := insertDelivery(ctx, tx, uuidNew(), subID, ev, "app.deleted", body); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insertDelivery(ctx, tx, uuidNew(), subID, ev, "app.deleted", body); err != nil {
		t.Fatalf("second insert must be a no-op, got: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countDeliveries(ctx, t, pool, subID); got != 1 {
		t.Fatalf("delivery count = %d, want 1 (deduped)", got)
	}
}

func TestEnqueuePanicIsolatedAuditStillCommits(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "erin")
	teamID, _ := seedTeam(ctx, t, pool, "epsilon", "Epsilon")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})

	obs := &countingObserver{}
	repo := dbpkg.NewRepository(pool)
	enq := NewEnqueuer(obs)
	enq.onBeforeInsert = func() { panic("simulated catalog panic") }
	repo.SetAuditEnqueuer(enq)
	svc := audit.NewService(repo, repo, nil)

	subj := "22222222-2222-2222-2222-222222222222"
	err := svc.Log(audit.WithTraceID(ctx, "11111111111111111111111111111111"), audit.Entry{
		TeamID:      teamID,
		ActorUserID: &userID,
		Source:      audit.SourceUser,
		SubjectType: "app",
		SubjectID:   &subj,
		Action:      "delete_app_confirm",
		Outcome:     audit.OutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("audit Log must succeed despite enqueue panic: %v", err)
	}

	// Audit row committed; delivery did NOT (panic isolated, metric recorded).
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE team_id = $1 AND action = 'delete_app_confirm'`, teamID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit row count = %d, want 1 (audit is the hard guarantee)", auditCount)
	}
	if got := countDeliveries(ctx, t, pool, subID); got != 0 {
		t.Fatalf("delivery count = %d, want 0 (enqueue panic isolated)", got)
	}
	if obs.panics == 0 {
		t.Error("expected panic_isolated metric to be observed")
	}
}

type countingObserver struct {
	enqueued int
	skipped  int
	panics   int
}

func (c *countingObserver) ObserveEnqueue(outcome string) {
	switch outcome {
	case "enqueued":
		c.enqueued++
	case "skipped":
		c.skipped++
	case "panic_isolated":
		c.panics++
	}
}
func (c *countingObserver) ObserveDelivery(string, string) {}
func (c *countingObserver) ObserveCircuitBreaker()         {}

func TestEnqueueAuditRollbackDropsDelivery(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "dave")
	teamID, _ := seedTeam(ctx, t, pool, "delta", "Delta")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "https://hooks.example.com/x", []string{"app.deleted"})

	svc := auditRepoWithEnqueuer(t, pool, nil)
	// A bad (non-UUID) subject id makes InsertAuditLog fail before commit, so the
	// shared tx rolls back — and with it the enqueued delivery (hard rule #3).
	bad := "not-a-uuid"
	err := svc.Log(ctx, audit.Entry{
		TeamID:      teamID,
		ActorUserID: &userID,
		Source:      audit.SourceUser,
		SubjectType: "app",
		SubjectID:   &bad,
		Action:      "delete_app_confirm",
		Outcome:     audit.OutcomeSuccess,
	})
	if err == nil {
		t.Fatal("expected audit Log to fail on bad subject id")
	}
	if got := countDeliveries(ctx, t, pool, subID); got != 0 {
		t.Fatalf("delivery count = %d, want 0 (audit rolled back)", got)
	}
}
