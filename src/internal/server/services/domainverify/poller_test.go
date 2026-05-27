package domainverify

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeLeader struct{ leader bool }

func (f fakeLeader) IsLeader(_ context.Context) bool { return f.leader }

func TestRunOnceSkipsWhenNotLeader(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: &fakeResolver{},
		Cloudflare: &fakeCloudflare{}, Auditor: &fakeAuditor{},
		Leader: fakeLeader{leader: false}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	// Seed an expired pending row.
	expired := Binding{
		ID: "b1", Hostname: "app.example.com", Status: StatusPending,
		ExpiresAt: now.Add(-time.Hour),
	}
	_ = store.Insert(context.Background(), expired)
	if err := poller.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.byID["b1"].Status != StatusPending {
		t.Fatalf("non-leader should not change state, got %s", store.byID["b1"].Status)
	}
}

func TestRunOnceVerifyPendingMarksVerified(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	pending := Binding{
		ID: "b1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusPending,
		VerificationToken: "tok", ExpiresAt: now.Add(time.Hour), CFHostnameID: "cf-1",
	}
	_ = store.Insert(context.Background(), pending)
	resolver := &fakeResolver{
		cname: map[string]string{"app.example.com": "tunnel-abc.cfargotunnel.com."},
		txt:   map[string][]string{"_0ops-verify.app.example.com": {"tok"}},
	}
	cf := &fakeCloudflare{}
	auditor := &fakeAuditor{}
	svc := newServiceForTest(store, cf, resolver, auditor, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: resolver,
		Cloudflare: cf, Auditor: auditor,
		Leader: fakeLeader{leader: true}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err := poller.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.byID["b1"].Status != StatusVerified {
		t.Fatalf("status=%s, want verified", store.byID["b1"].Status)
	}
}

func TestRunOnceCleanupExpiredMarksRowExpired(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	overdue := Binding{
		ID: "b1", Hostname: "stale.example.com",
		Kind: "extra", Status: StatusPending,
		VerificationToken: "tok", ExpiresAt: now.Add(-time.Hour),
	}
	_ = store.Insert(context.Background(), overdue)
	// Pending tokens fail dual-condition without DNS records; verifyPending
	// leaves them in pending. cleanupExpired then sweeps them.
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: &fakeResolver{},
		Cloudflare: &fakeCloudflare{}, Auditor: &fakeAuditor{},
		Leader: fakeLeader{leader: true}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err := poller.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.byID["b1"].Status != StatusExpired {
		t.Fatalf("status=%s, want expired", store.byID["b1"].Status)
	}
}

func TestRunOnceCheckUnhealthyMarksFailure(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	verified := Binding{
		ID: "b1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusVerified, Verified: true,
		VerificationToken: "tok", IsApex: false,
		CFHostnameID: "cf-1",
	}
	_ = store.Insert(context.Background(), verified)
	resolver := &fakeResolver{
		// Hostname no longer resolves to tunnel target.
		cname: map[string]string{"app.example.com": "elsewhere.example.net."},
		txt:   map[string][]string{"_0ops-verify.app.example.com": {"tok"}},
	}
	svc := newServiceForTest(store, &fakeCloudflare{}, resolver, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: resolver,
		Cloudflare: &fakeCloudflare{}, Auditor: &fakeAuditor{},
		Leader: fakeLeader{leader: true}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err := poller.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.byID["b1"].Status != StatusUnhealthy {
		t.Fatalf("status=%s, want unhealthy", store.byID["b1"].Status)
	}
	if store.byID["b1"].HealthCheckFailedAt == nil {
		t.Fatal("expected HealthCheckFailedAt set")
	}
}

func TestRunOnceCheckUnhealthyReleasesAfter7Days(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	failedAt := now.Add(-(7*24*time.Hour + time.Minute))
	verified := Binding{
		ID: "b1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusUnhealthy, Verified: true,
		VerificationToken: "tok", IsApex: false,
		HealthCheckFailedAt: &failedAt,
		CFHostnameID:        "cf-1",
	}
	_ = store.Insert(context.Background(), verified)
	resolver := &fakeResolver{
		cname: map[string]string{"app.example.com": "elsewhere.example.net."},
		txt:   map[string][]string{"_0ops-verify.app.example.com": {"tok"}},
	}
	deleted := ""
	cf := &fakeCloudflare{delete: func(_ context.Context, id string) error {
		deleted = id
		return nil
	}}
	svc := newServiceForTest(store, cf, resolver, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: resolver,
		Cloudflare: cf, Auditor: &fakeAuditor{},
		Leader: fakeLeader{leader: true}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err := poller.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.byID["b1"].Status != StatusReleased {
		t.Fatalf("status=%s, want released", store.byID["b1"].Status)
	}
	if deleted != "cf-1" {
		t.Fatalf("expected DeleteCustomHostname(cf-1), got %q", deleted)
	}
}

func TestRunOnceCheckUnhealthyClearsOnRecovery(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	failedAt := now.Add(-24 * time.Hour)
	verified := Binding{
		ID: "b1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusUnhealthy, Verified: true,
		VerificationToken: "tok", IsApex: false,
		HealthCheckFailedAt: &failedAt,
		CFHostnameID:        "cf-1",
	}
	_ = store.Insert(context.Background(), verified)
	resolver := &fakeResolver{
		cname: map[string]string{"app.example.com": "tunnel-abc.cfargotunnel.com."},
		txt:   map[string][]string{"_0ops-verify.app.example.com": {"tok"}},
	}
	svc := newServiceForTest(store, &fakeCloudflare{}, resolver, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: resolver,
		Cloudflare: &fakeCloudflare{}, Auditor: &fakeAuditor{},
		Leader: fakeLeader{leader: true}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
	})
	if err := poller.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.byID["b1"].Status != StatusVerified {
		t.Fatalf("status=%s, want verified", store.byID["b1"].Status)
	}
	if store.byID["b1"].HealthCheckFailedAt != nil {
		t.Fatalf("HealthCheckFailedAt should be cleared")
	}
}

func TestRunLoopStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	poller := NewPoller(PollerConfig{
		Service: svc, Store: store, Resolver: &fakeResolver{},
		Cloudflare: &fakeCloudflare{}, Auditor: &fakeAuditor{},
		Leader: fakeLeader{leader: true}, Now: fixedNow(now),
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
		Tick:         time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		poller.RunLoop(ctx)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	wg.Wait()
}
