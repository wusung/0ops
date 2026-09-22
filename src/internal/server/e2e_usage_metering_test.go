package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
	"github.com/wusung/0ops/internal/server/services/usage"
	"github.com/wusung/0ops/internal/shared/backendclient"
	"github.com/wusung/0ops/internal/shared/token"
)

// TestUsageMeteringEndToEnd drives the feature's headline guarantee
// through the assembled system: a pod starts and stops on the cluster,
// and the team can see exactly what it was allocated and for how long.
//
// Everything downstream of the cluster is real — the reconciler, the
// ledger tables in Postgres, the rollup integration, the router, RBAC,
// and the HTTP contract. Only the Kubernetes API is substituted, which
// AGENTS.md permits for an external dependency; the ledger rows are
// produced by the real reconcile path, never written by the test.
//
// Coverage maps to AGENTS.md "高風險區域必測":
//   - team 隔離 (another team's token sees nothing)
//   - reconciler 收斂 (a vanished pod is closed, conservatively)
//   - idempotent retry (a repeated pass does not double-bill)
func TestUsageMeteringEndToEnd(t *testing.T) {
	repo, pool := usageE2ERepo(t)
	ctx := context.Background()

	teamID, teamSlug := seedE2ETeam(t, pool, "usage-e2e")
	userID := seedE2EUser(t, pool, "usage-e2e")
	seedE2EMembership(t, pool, teamID, userID, "owner")
	appSlug := "web"
	seedE2EApp(t, pool, teamID, appSlug)
	bearer := seedE2EToken(t, pool, teamID, userID)

	srv := httptest.NewServer(NewRouter(repo))
	t.Cleanup(srv.Close)
	client := backendclient.New(srv.URL, bearer)

	// A pod that ran for exactly 40 seconds earlier today. Anchored to
	// the current UTC day rather than "an hour ago": run the suite at
	// 00:30 UTC and an hour ago is yesterday, which has no rollup yet.
	started := earlierToday(time.Hour)
	finished := started.Add(40 * time.Second)
	pod := k3s.PodSnapshot{
		UID:           uuid.NewString(),
		Name:          appSlug + "-abc123",
		Namespace:     "team-" + teamSlug,
		AppSlug:       appSlug,
		TeamSlug:      teamSlug,
		StartedAt:     &started,
		CPUMillicores: 500,
		MemoryBytes:   512 * 1024 * 1024,
	}

	lister := &scriptedCluster{pods: []k3s.PodSnapshot{pod}}
	rec := usage.NewReconciler(lister, repo, nil, nil, nil)

	// Pass 1: the pod is running.
	if _, err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile while running: %v", err)
	}

	// Pass 2: it has terminated. K8s still reports the object, carrying
	// the authoritative finish time.
	terminated := pod
	terminated.TerminatedAt = &finished
	lister.pods = []k3s.PodSnapshot{terminated}
	if _, err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile after termination: %v", err)
	}

	// Pass 3: the object is gone. Nothing should change — the interval
	// is already closed with a real timestamp.
	lister.pods = nil
	if _, err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile after deletion: %v", err)
	}

	resp, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{Interval: true})
	if err != nil {
		t.Fatalf("GetAppUsage: %v", err)
	}
	if len(resp.Apps) != 1 {
		t.Fatalf("want usage for 1 app, got %d", len(resp.Apps))
	}
	got := resp.Apps[0].Totals

	// 500m for 40s = 20 core-seconds. Exact, not approximate: this is
	// the number a 5-minute sampler could never produce for a pod that
	// lived under a minute.
	if want := int64(500 * 40); got.CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", got.CPUMillicoreSeconds, want)
	}
	if got.PodSeconds != 40 {
		t.Errorf("PodSeconds = %d, want 40", got.PodSeconds)
	}
	if want := fmt.Sprint(int64(512*1024*1024) * 40); got.MemoryByteSeconds != want {
		t.Errorf("MemoryByteSeconds = %s, want %s", got.MemoryByteSeconds, want)
	}
	if got.EstimatedSeconds != 0 {
		t.Errorf("EstimatedSeconds = %d; an observed termination must not be flagged as inferred", got.EstimatedSeconds)
	}
	if len(resp.Intervals) != 1 || resp.Intervals[0].Estimated {
		t.Errorf("interval detail wrong: %+v", resp.Intervals)
	}
	if !strings.Contains(resp.BillingDisclaimer, "not a bill") {
		t.Errorf("response must state it is not a bill, got %q", resp.BillingDisclaimer)
	}

	// Re-running the reconcile must not change the bill.
	for i := 0; i < 3; i++ {
		if _, err := rec.ReconcileOnce(ctx); err != nil {
			t.Fatalf("idempotent pass %d: %v", i, err)
		}
	}
	after, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{})
	if err != nil {
		t.Fatalf("GetAppUsage after replays: %v", err)
	}
	if after.Apps[0].Totals.CPUMillicoreSeconds != got.CPUMillicoreSeconds ||
		after.Apps[0].Totals.PodSeconds != got.PodSeconds {
		t.Fatalf("repeated reconcile changed the totals: %+v → %+v", got, after.Apps[0].Totals)
	}

	// A second team must not see any of it.
	otherTeamID, otherSlug := seedE2ETeam(t, pool, "usage-e2e-other")
	otherUser := seedE2EUser(t, pool, "usage-e2e-other")
	seedE2EMembership(t, pool, otherTeamID, otherUser, "owner")
	otherBearer := seedE2EToken(t, pool, otherTeamID, otherUser)

	otherResp, err := backendclient.New(srv.URL, otherBearer).
		GetTeamUsage(ctx, otherSlug, backendclient.UsageParams{})
	if err != nil {
		t.Fatalf("GetTeamUsage for the other team: %v", err)
	}
	if len(otherResp.Apps) != 0 {
		t.Fatalf("team %s saw %d apps belonging to another team", otherSlug, len(otherResp.Apps))
	}
}

// TestUsageMeteringClosesVanishedPodConservatively covers the other
// branch: the backend never saw the pod stop, so it bills only up to the
// last moment it knows the pod was alive.
func TestUsageMeteringClosesVanishedPodConservatively(t *testing.T) {
	repo, pool := usageE2ERepo(t)
	ctx := context.Background()

	teamID, teamSlug := seedE2ETeam(t, pool, "usage-gone")
	userID := seedE2EUser(t, pool, "usage-gone")
	seedE2EMembership(t, pool, teamID, userID, "owner")
	appSlug := "worker"
	seedE2EApp(t, pool, teamID, appSlug)
	bearer := seedE2EToken(t, pool, teamID, userID)

	srv := httptest.NewServer(NewRouter(repo))
	t.Cleanup(srv.Close)
	client := backendclient.New(srv.URL, bearer)

	started := earlierToday(30 * time.Minute)
	pod := k3s.PodSnapshot{
		UID: uuid.NewString(), Name: appSlug + "-x", Namespace: "team-" + teamSlug,
		AppSlug: appSlug, TeamSlug: teamSlug, StartedAt: &started, CPUMillicores: 100,
	}
	lister := &scriptedCluster{pods: []k3s.PodSnapshot{pod}}
	rec := usage.NewReconciler(lister, repo, nil, nil, nil)

	if _, err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile while running: %v", err)
	}
	// The pod disappears without ever reporting a termination.
	lister.pods = nil
	if _, err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile after vanish: %v", err)
	}

	resp, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{Interval: true})
	if err != nil {
		t.Fatalf("GetAppUsage: %v", err)
	}
	if len(resp.Intervals) != 1 {
		t.Fatalf("want 1 interval, got %d", len(resp.Intervals))
	}
	in := resp.Intervals[0]
	if !in.Estimated || in.CloseReason == nil || *in.CloseReason != db.CloseReasonReconciledMissing {
		t.Errorf("a pod that vanished unseen must be flagged: %+v", in)
	}
	if resp.Apps[0].Totals.EstimatedRatio != 1 {
		t.Errorf("EstimatedRatio = %v, want 1 — the whole window is inferred",
			resp.Apps[0].Totals.EstimatedRatio)
	}
	if in.EndedAt == nil {
		t.Fatal("interval left open")
	}
	// Must not bill past the last sighting.
	ended, err := time.Parse(time.RFC3339, *in.EndedAt)
	if err != nil {
		t.Fatalf("parse ended_at: %v", err)
	}
	if ended.After(time.Now().UTC()) {
		t.Errorf("ended_at %v is in the future", ended)
	}
}

// scriptedCluster stands in for the Kubernetes API. The test rewrites
// `pods` between passes to script a pod's life.
type scriptedCluster struct {
	pods  []k3s.PodSnapshot
	usage []k3s.PodUsage
	// usageErr simulates a cluster with no metrics-server.
	usageErr error
}

func (s *scriptedCluster) ListManagedPods(context.Context) ([]k3s.PodSnapshot, error) {
	return s.pods, nil
}

func (s *scriptedCluster) ListPodUsage(context.Context) ([]k3s.PodUsage, error) {
	return s.usage, s.usageErr
}

// earlierToday returns a timestamp `ago` in the past, but never earlier
// than the start of the current UTC day. Without the floor these tests
// fail for one hour every night, when "an hour ago" lands on a day that
// has not been rolled up yet.
func earlierToday(ago time.Duration) time.Time {
	now := time.Now().UTC().Truncate(time.Second)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	at := now.Add(-ago)
	if at.Before(dayStart) {
		at = dayStart.Add(time.Second)
	}
	return at
}

func usageE2ERepo(t *testing.T) (*db.Repository, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = strings.ReplaceAll(os.Getenv("DATABASE_URL"), "@db:5432", "@127.0.0.1:15432")
	}
	if dsn == "" {
		t.Skip("DATABASE_URL is required for the usage metering e2e")
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, db.Config{URL: dsn})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return db.NewRepository(pool), pool
}

func seedE2ETeam(t *testing.T, pool *pgxpool.Pool, prefix string) (id, slug string) {
	t.Helper()
	slug = fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
	err := pool.QueryRow(context.Background(),
		`INSERT INTO team (slug, name) VALUES ($1, $2) RETURNING id::text`, slug, slug).Scan(&id)
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM team WHERE id = $1::uuid`, id) })
	return id, slug
}

func seedE2EUser(t *testing.T, pool *pgxpool.Pool, prefix string) string {
	t.Helper()
	var id string
	login := fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
	err := pool.QueryRow(context.Background(),
		`INSERT INTO user_account (github_login) VALUES ($1) RETURNING id::text`, login).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM user_account WHERE id = $1::uuid`, id) })
	return id
}

func seedE2EMembership(t *testing.T, pool *pgxpool.Pool, teamID, userID, role string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO team_membership (team_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`,
		teamID, userID, role)
	if err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

func seedE2EApp(t *testing.T, pool *pgxpool.Pool, teamID, slug string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO app (team_id, slug, repo_url, status) VALUES ($1::uuid, $2, 'file:///workspace/x', 'live') RETURNING id::text`,
		teamID, slug).Scan(&id)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM app WHERE id = $1::uuid`, id) })
	return id
}

// seedE2EToken mints a real PAT and stores its argon2id hash, so the
// request goes through the same auth path production does.
func seedE2EToken(t *testing.T, pool *pgxpool.Pool, teamID, userID string) string {
	t.Helper()
	tokenID := uuid.NewString()
	secret, err := token.NewBearerTokenSecret()
	if err != nil {
		t.Fatalf("token secret: %v", err)
	}
	hash, err := token.HashBearerToken(secret)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
INSERT INTO cli_token (id, owner_user_id, team_id, token_hash, name, scopes, kind, expires_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'usage-e2e', $5, 'pat', $6)`,
		tokenID, userID, teamID, hash, []string{"apps:read"}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("seed token: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM cli_token WHERE id = $1::uuid`, tokenID) })
	return token.FormatBearerToken("pat", tokenID, secret)
}

// TestUsageMeteringRollsUpClosedDays covers the other half of the read
// path: once a day closes, the numbers come from the permanent rollup
// rather than from live intervals, and they must match what the live
// view reported.
func TestUsageMeteringRollsUpClosedDays(t *testing.T) {
	repo, pool := usageE2ERepo(t)
	ctx := context.Background()

	teamID, teamSlug := seedE2ETeam(t, pool, "usage-rollup")
	userID := seedE2EUser(t, pool, "usage-rollup")
	seedE2EMembership(t, pool, teamID, userID, "owner")
	appSlug := "batch"
	seedE2EApp(t, pool, teamID, appSlug)
	bearer := seedE2EToken(t, pool, teamID, userID)

	srv := httptest.NewServer(NewRouter(repo))
	t.Cleanup(srv.Close)
	client := backendclient.New(srv.URL, bearer)

	// A pod that ran for 10 minutes yesterday.
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	started := yesterday.Add(9 * time.Hour)
	finished := started.Add(10 * time.Minute)
	pod := k3s.PodSnapshot{
		UID: uuid.NewString(), Name: appSlug + "-1", Namespace: "team-" + teamSlug,
		AppSlug: appSlug, TeamSlug: teamSlug, StartedAt: &started,
		CPUMillicores: 250, MemoryBytes: 128 * 1024 * 1024,
	}
	terminated := pod
	terminated.TerminatedAt = &finished

	rec := usage.NewReconciler(&scriptedCluster{pods: []k3s.PodSnapshot{terminated}}, repo, nil, nil, nil)
	if _, err := rec.ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	roll := usage.NewRollupper(repo, nil, nil, nil, 0, 0)
	if _, err := roll.Tick(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	// Re-running must not double count.
	if _, err := roll.Tick(ctx); err != nil {
		t.Fatalf("rollup replay: %v", err)
	}

	resp, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{
		From: yesterday.Format(time.DateOnly),
		To:   yesterday.Format(time.DateOnly),
	})
	if err != nil {
		t.Fatalf("GetAppUsage: %v", err)
	}
	if len(resp.Apps) != 1 || len(resp.Apps[0].Days) != 1 {
		t.Fatalf("want one app with one day, got %+v", resp.Apps)
	}
	day := resp.Apps[0].Days[0]
	if day.Source != "rollup" {
		t.Errorf("closed day served from %q, want the permanent rollup", day.Source)
	}
	if want := int64(250 * 600); day.Totals.CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", day.Totals.CPUMillicoreSeconds, want)
	}
	if day.Totals.PodSeconds != 600 {
		t.Errorf("PodSeconds = %d, want 600", day.Totals.PodSeconds)
	}
}

// TestUsageMeteringObservedTrackIsSeparate covers the observation track:
// measured consumption reaches the API in its own field, and its absence
// never disturbs the allocation figures.
func TestUsageMeteringObservedTrackIsSeparate(t *testing.T) {
	repo, pool := usageE2ERepo(t)
	ctx := context.Background()

	teamID, teamSlug := seedE2ETeam(t, pool, "usage-obs")
	userID := seedE2EUser(t, pool, "usage-obs")
	seedE2EMembership(t, pool, teamID, userID, "owner")
	appSlug := "api"
	seedE2EApp(t, pool, teamID, appSlug)
	bearer := seedE2EToken(t, pool, teamID, userID)

	srv := httptest.NewServer(NewRouter(repo))
	t.Cleanup(srv.Close)
	client := backendclient.New(srv.URL, bearer)

	started := earlierToday(time.Hour)
	podUID := uuid.NewString()
	pod := k3s.PodSnapshot{
		UID: podUID, Name: appSlug + "-1", Namespace: "team-" + teamSlug,
		AppSlug: appSlug, TeamSlug: teamSlug, StartedAt: &started, Ready: true,
		CPUMillicores: 1000, MemoryBytes: 1 << 30,
	}
	cluster := &scriptedCluster{
		pods: []k3s.PodSnapshot{pod},
		// Reserved a full core and a gig; actually using a fraction.
		usage: []k3s.PodUsage{{UID: podUID, CPUMillicores: 35, MemoryBytes: 120 << 20}},
	}

	if _, err := usage.NewReconciler(cluster, repo, nil, nil, nil).ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	sampler := usage.NewSampler(cluster, repo, nil, nil, nil)
	if _, err := sampler.Tick(ctx); err != nil {
		t.Fatalf("sample: %v", err)
	}

	resp, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{Observed: true})
	if err != nil {
		t.Fatalf("GetAppUsage: %v", err)
	}
	if len(resp.Apps) != 1 {
		t.Fatalf("want 1 app, got %d", len(resp.Apps))
	}
	app := resp.Apps[0]
	if app.Observed == nil {
		t.Fatal("observed block missing")
	}
	if app.Observed.CPUMillicoresMax != 35 || app.Observed.MemoryBytesMax != 120<<20 {
		t.Errorf("measured consumption = %+v", app.Observed)
	}
	// Allocation still reports the reserved core, not the 35m used.
	if app.Totals.PodSeconds == 0 {
		t.Fatal("allocation figures missing")
	}
	expectedCPUSeconds := int64(1000) * app.Totals.PodSeconds
	if app.Totals.CPUMillicoreSeconds != expectedCPUSeconds {
		t.Errorf("allocation = %d millicore-seconds, want %d (reserved, not consumed)",
			app.Totals.CPUMillicoreSeconds, expectedCPUSeconds)
	}

	// Without asking, no observation appears.
	plain, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{})
	if err != nil {
		t.Fatalf("GetAppUsage plain: %v", err)
	}
	if plain.Apps[0].Observed != nil {
		t.Error("observation must be opt-in")
	}
}

// A cluster without metrics-server must still meter correctly: the
// ledger does not depend on the observation track at all.
func TestUsageMeteringSurvivesMissingMetricsServer(t *testing.T) {
	repo, pool := usageE2ERepo(t)
	ctx := context.Background()

	teamID, teamSlug := seedE2ETeam(t, pool, "usage-nometrics")
	userID := seedE2EUser(t, pool, "usage-nometrics")
	seedE2EMembership(t, pool, teamID, userID, "owner")
	appSlug := "svc"
	seedE2EApp(t, pool, teamID, appSlug)
	bearer := seedE2EToken(t, pool, teamID, userID)

	srv := httptest.NewServer(NewRouter(repo))
	t.Cleanup(srv.Close)
	client := backendclient.New(srv.URL, bearer)

	started := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	finished := started.Add(time.Hour)
	podUID := uuid.NewString()
	pod := k3s.PodSnapshot{
		UID: podUID, Name: appSlug + "-1", Namespace: "team-" + teamSlug,
		AppSlug: appSlug, TeamSlug: teamSlug, StartedAt: &started,
		TerminatedAt: &finished, CPUMillicores: 200,
	}
	cluster := &scriptedCluster{
		pods:     []k3s.PodSnapshot{pod},
		usageErr: k3s.ErrMetricsAPIUnavailable,
	}

	if _, err := usage.NewReconciler(cluster, repo, nil, nil, nil).ReconcileOnce(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	sampler := usage.NewSampler(cluster, repo, nil, nil, nil)
	written, err := sampler.Tick(ctx)
	if err != nil {
		t.Fatalf("a missing metrics API must not fail the tick: %v", err)
	}
	if written != 0 {
		t.Fatalf("wrote %d observations without a metrics API", written)
	}

	resp, err := client.GetAppUsage(ctx, teamSlug, appSlug, backendclient.UsageParams{Observed: true})
	if err != nil {
		t.Fatalf("GetAppUsage: %v", err)
	}
	// Metering is exact regardless: 200m for one hour.
	if want := int64(200 * 3600); resp.Apps[0].Totals.CPUMillicoreSeconds != want {
		t.Errorf("CPUMillicoreSeconds = %d, want %d", resp.Apps[0].Totals.CPUMillicoreSeconds, want)
	}
	if resp.Apps[0].Observed != nil {
		t.Error("nothing was measured; the block must be absent rather than zeroed")
	}
}
