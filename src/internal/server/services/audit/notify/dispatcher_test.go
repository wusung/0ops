package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

// insertPendingDelivery inserts a pending delivery directly (bypassing the
// audit path) so dispatcher tests can drive the state machine in isolation.
func insertPendingDelivery(ctx context.Context, t *testing.T, pool *pgxpool.Pool, subID, teamID, event string) string {
	t.Helper()
	body := []byte(`{"event":"` + event + `","delivery_id":"x"}`)
	var id string
	err := pool.QueryRow(ctx, `
INSERT INTO webhook_delivery (subscription_id, team_id, audit_log_id, event, payload, status)
VALUES ($1, $2, $3, $4, $5::jsonb, 'pending')
RETURNING id
`, subID, teamID, 1, event, body).Scan(&id)
	if err != nil {
		t.Fatalf("insert pending delivery: %v", err)
	}
	return id
}

// makeDue resets next_attempt_at into the past so the next Tick reclaims a
// failed delivery without waiting out the real backoff window.
func makeDue(ctx context.Context, t *testing.T, pool *pgxpool.Pool, deliveryID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE webhook_delivery SET next_attempt_at = now() - interval '1 hour' WHERE id = $1`, deliveryID); err != nil {
		t.Fatalf("make due: %v", err)
	}
}

func deliveryState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id string) (status string, attempt int) {
	t.Helper()
	if err := pool.QueryRow(ctx, `SELECT status, attempt FROM webhook_delivery WHERE id = $1`, id).Scan(&status, &attempt); err != nil {
		t.Fatalf("read delivery state: %v", err)
	}
	return status, attempt
}

func newDispatcher(pool *pgxpool.Pool, client *http.Client, lg AuditLogger) *Dispatcher {
	return NewDispatcher(DispatcherConfig{
		Pool:   pool,
		Client: client,
		Audit:  lg,
		Rand:   func() float64 { return 0.5 },
	})
}

func TestDispatcherDeliversSignedAndSucceeds(t *testing.T) {
	pool, ctx := newTestPool(t)
	teamID, _ := seedTeam(ctx, t, pool, "disp", "Disp")
	subID := seedSubscription(ctx, t, pool, teamID, "", []string{"app.deleted"})

	var gotSig, gotTs, gotDelivery, gotEvent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(HeaderSignature)
		gotTs = r.Header.Get(HeaderTimestamp)
		gotDelivery = r.Header.Get(HeaderDelivery)
		gotEvent = r.Header.Get(HeaderEvent)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setSubURL(ctx, t, pool, subID, srv.URL)

	delID := insertPendingDelivery(ctx, t, pool, subID, teamID, "app.deleted")
	d := newDispatcher(pool, srv.Client(), nil)
	if n, err := d.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("Tick = (%d,%v), want (1,nil)", n, err)
	}

	status, attempt := deliveryState(ctx, t, pool, delID)
	if status != "succeeded" || attempt != 1 {
		t.Fatalf("state = (%s, attempt %d), want (succeeded, 1)", status, attempt)
	}
	if gotSig == "" || gotTs == "" {
		t.Error("missing signature/timestamp headers")
	}
	if gotDelivery != delID {
		t.Errorf("X-0ops-Delivery = %q, want %q", gotDelivery, delID)
	}
	if gotEvent != "app.deleted" {
		t.Errorf("X-0ops-Event = %q", gotEvent)
	}
	// Delivery attempts must not write audit_log (hard rule #5).
	if got := auditCountForTeam(ctx, t, pool, teamID); got != 0 {
		t.Errorf("audit rows after delivery = %d, want 0", got)
	}
}

func TestDispatcherRetriesThenSucceeds(t *testing.T) {
	pool, ctx := newTestPool(t)
	teamID, _ := seedTeam(ctx, t, pool, "retry", "Retry")
	subID := seedSubscription(ctx, t, pool, teamID, "", []string{"app.deleted"})

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setSubURL(ctx, t, pool, subID, srv.URL)

	delID := insertPendingDelivery(ctx, t, pool, subID, teamID, "app.deleted")
	d := newDispatcher(pool, srv.Client(), nil)

	// Tick 1 (500) → failed; Tick 2 (500) → failed; Tick 3 (200) → succeeded.
	for i := 0; i < 3; i++ {
		makeDue(ctx, t, pool, delID)
		if _, err := d.Tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	status, attempt := deliveryState(ctx, t, pool, delID)
	if status != "succeeded" || attempt != 3 {
		t.Fatalf("state = (%s, attempt %d), want (succeeded, 3)", status, attempt)
	}
}

func TestDispatcherDropsAtMaxAttempts(t *testing.T) {
	pool, ctx := newTestPool(t)
	teamID, _ := seedTeam(ctx, t, pool, "drop", "Drop")
	subID := seedSubscription(ctx, t, pool, teamID, "", []string{"app.deleted"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	setSubURL(ctx, t, pool, subID, srv.URL)

	delID := insertPendingDelivery(ctx, t, pool, subID, teamID, "app.deleted")
	d := newDispatcher(pool, srv.Client(), nil)
	for i := 0; i < DefaultMaxAttempts+2; i++ {
		makeDue(ctx, t, pool, delID)
		_, _ = d.Tick(ctx)
	}
	status, attempt := deliveryState(ctx, t, pool, delID)
	if status != "dropped" || attempt != DefaultMaxAttempts {
		t.Fatalf("state = (%s, attempt %d), want (dropped, %d)", status, attempt, DefaultMaxAttempts)
	}
}

func TestDispatcherCircuitBreakerDisablesAndAudits(t *testing.T) {
	pool, ctx := newTestPool(t)
	userID := seedUser(ctx, t, pool, "owner")
	teamID, _ := seedTeam(ctx, t, pool, "breaker", "Breaker")
	seedMembership(ctx, t, pool, teamID, userID, "owner")
	subID := seedSubscription(ctx, t, pool, teamID, "", []string{"app.deleted"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	setSubURL(ctx, t, pool, subID, srv.URL)

	// Pre-load the subscription to one failure below the breaker threshold so a
	// single failing delivery trips it.
	if _, err := pool.Exec(ctx, `UPDATE webhook_subscription SET consecutive_failures = $2 WHERE id = $1`, subID, breakerThreshold-1); err != nil {
		t.Fatal(err)
	}

	repo := dbpkg.NewRepository(pool)
	auditSvc := audit.NewService(repo, repo, nil)
	delID := insertPendingDelivery(ctx, t, pool, subID, teamID, "app.deleted")
	d := newDispatcher(pool, srv.Client(), auditSvc)
	if _, err := d.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var active bool
	var reason *string
	if err := pool.QueryRow(ctx, `SELECT active, disabled_reason FROM webhook_subscription WHERE id = $1`, subID).Scan(&active, &reason); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("subscription should be auto-disabled after breaker trip")
	}
	if reason == nil || *reason != "auto_circuit_breaker" {
		t.Fatalf("disabled_reason = %v, want auto_circuit_breaker", reason)
	}

	var auditN int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE team_id = $1 AND action = $2`, teamID, ActionSubscriptionDisabled).Scan(&auditN); err != nil {
		t.Fatal(err)
	}
	if auditN != 1 {
		t.Fatalf("webhook_subscription_disabled audit rows = %d, want 1", auditN)
	}
	_ = delID
}

func TestDispatcherReclaimsStaleDelivering(t *testing.T) {
	pool, ctx := newTestPool(t)
	teamID, _ := seedTeam(ctx, t, pool, "stale", "Stale")
	subID := seedSubscription(ctx, t, pool, teamID, "", []string{"app.deleted"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setSubURL(ctx, t, pool, subID, srv.URL)

	// Simulate a worker that crashed mid-delivery: the row is stuck in
	// 'delivering' with an elapsed lease. A later tick must reclaim and deliver
	// it rather than stranding it forever.
	delID := insertPendingDelivery(ctx, t, pool, subID, teamID, "app.deleted")
	if _, err := pool.Exec(ctx, `UPDATE webhook_delivery SET status='delivering', next_attempt_at = now() - interval '10 minutes' WHERE id=$1`, delID); err != nil {
		t.Fatal(err)
	}

	d := newDispatcher(pool, srv.Client(), nil)
	if n, err := d.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("Tick = (%d,%v), want (1,nil) — stale delivering row must be reclaimed", n, err)
	}
	if status, _ := deliveryState(ctx, t, pool, delID); status != "succeeded" {
		t.Fatalf("reclaimed delivery status = %s, want succeeded", status)
	}
}

func setSubURL(ctx context.Context, t *testing.T, pool *pgxpool.Pool, subID, url string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE webhook_subscription SET url = $2 WHERE id = $1`, subID, url); err != nil {
		t.Fatalf("set sub url: %v", err)
	}
}

func auditCountForTeam(ctx context.Context, t *testing.T, pool *pgxpool.Pool, teamID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE team_id = $1`, teamID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}
