package domainverify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu       sync.Mutex
	byHost   map[string]*Binding
	byID     map[string]*Binding
	inserted []Binding
	updated  []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		byHost: make(map[string]*Binding),
		byID:   make(map[string]*Binding),
	}
}

func (s *fakeStore) GetByHostname(_ context.Context, host string) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byHost[host]
	if !ok {
		return Binding{}, ErrBindingNotFound
	}
	return *b, nil
}

func (s *fakeStore) Insert(_ context.Context, b Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byHost[b.Hostname]; dup {
		return ErrHostnameTaken
	}
	cp := b
	s.byHost[b.Hostname] = &cp
	s.byID[b.ID] = &cp
	s.inserted = append(s.inserted, cp)
	return nil
}

func (s *fakeStore) SetCloudflareHostnameID(_ context.Context, id, cfID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	b.CFHostnameID = cfID
	return nil
}

func (s *fakeStore) UpdateVerified(_ context.Context, id string, when time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	b.Status = StatusVerified
	b.Verified = true
	b.VerifiedAt = &when
	s.updated = append(s.updated, id+":verified")
	return nil
}

func (s *fakeStore) UpdateExpired(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	b.Status = StatusExpired
	s.updated = append(s.updated, id+":expired")
	return nil
}

func (s *fakeStore) UpdateExtendsUsed(_ context.Context, id string, used int, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	b.ExtendsUsed = used
	b.ExpiresAt = expiresAt
	s.updated = append(s.updated, id+":extend")
	return nil
}

func (s *fakeStore) UpdateUnhealthyMark(_ context.Context, id string, failedAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	b.HealthCheckFailedAt = failedAt
	if failedAt == nil {
		b.Status = StatusVerified
		s.updated = append(s.updated, id+":cleared")
	} else {
		b.Status = StatusUnhealthy
		s.updated = append(s.updated, id+":unhealthy")
	}
	return nil
}

func (s *fakeStore) UpdateReleased(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	b.Status = StatusReleased
	s.updated = append(s.updated, id+":released")
	return nil
}

func (s *fakeStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byID[id]
	if !ok {
		return ErrBindingNotFound
	}
	delete(s.byHost, b.Hostname)
	delete(s.byID, id)
	s.updated = append(s.updated, id+":deleted")
	return nil
}

func (s *fakeStore) ListPending(_ context.Context) ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Binding{}
	for _, b := range s.byID {
		if b.Status == StatusPending {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (s *fakeStore) ListVerified(_ context.Context) ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Binding{}
	for _, b := range s.byID {
		if b.Status == StatusVerified || b.Status == StatusUnhealthy {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (s *fakeStore) ListExpiredCandidates(_ context.Context) ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Binding{}
	for _, b := range s.byID {
		if b.Status == StatusPending {
			out = append(out, *b)
		}
	}
	return out, nil
}

type fakeCloudflare struct {
	register func(ctx context.Context, host string) (string, error)
	activate func(ctx context.Context, id string) error
	delete   func(ctx context.Context, id string) error
}

func (c *fakeCloudflare) RegisterCustomHostname(ctx context.Context, h string) (string, error) {
	if c.register != nil {
		return c.register(ctx, h)
	}
	return "cf-" + h, nil
}

func (c *fakeCloudflare) ActivateCustomHostname(ctx context.Context, id string) error {
	if c.activate != nil {
		return c.activate(ctx, id)
	}
	return nil
}

func (c *fakeCloudflare) DeleteCustomHostname(ctx context.Context, id string) error {
	if c.delete != nil {
		return c.delete(ctx, id)
	}
	return nil
}

type fakeAuditor struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (a *fakeAuditor) Record(_ context.Context, e AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *fakeAuditor) snapshot() []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEvent, len(a.events))
	copy(out, a.events)
	return out
}

type fakePlanGate struct{ allow bool }

func (p fakePlanGate) AllowExtra(_ string) bool { return p.allow }

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func fixedID(id string) func() string { return func() string { return id } }

func newServiceForTest(store Store, cf CloudflareHostnameAPI, resolver Resolver, auditor Auditor, plan PlanGate, now time.Time) *Service {
	return NewService(ServiceConfig{
		Store:        store,
		Cloudflare:   cf,
		Resolver:     resolver,
		Auditor:      auditor,
		PlanGate:     plan,
		TunnelTarget: "tunnel-abc.cfargotunnel.com",
		Now:          fixedNow(now),
		NewID:        fixedID("binding-1"),
	})
}

func TestServiceAddPlansNonApex(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	plan, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "app.example.com", PlanTier: "pro",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if plan.IsApex {
		t.Fatal("expected non-apex")
	}
	if plan.CNAMETarget != "tunnel-abc.cfargotunnel.com" {
		t.Fatalf("cname target=%q", plan.CNAMETarget)
	}
	if !strings.HasPrefix(plan.TXTName, "_0ops-verify.") {
		t.Fatalf("TXT name=%q", plan.TXTName)
	}
	if len(plan.TXTValue) != 64 {
		t.Fatalf("token len=%d, want 64", len(plan.TXTValue))
	}
	if !plan.ExpiresAt.Equal(now.UTC().Add(24 * time.Hour)) {
		t.Fatalf("expires=%s", plan.ExpiresAt)
	}
	if len(plan.ApexCompatibility) != 0 {
		t.Fatalf("apex compat list should be empty for non-apex, got %v", plan.ApexCompatibility)
	}
}

func TestServiceAddPlansApexIncludesProviderList(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	plan, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "example.com", PlanTier: "pro",
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !plan.IsApex {
		t.Fatal("expected apex")
	}
	if len(plan.ApexCompatibility) == 0 {
		t.Fatal("expected apex compat list non-empty")
	}
}

func TestServiceAddRejectsFreePlan(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(newFakeStore(), &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: false}, now)
	_, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "app.example.com", PlanTier: "free",
	})
	if !errors.Is(err, ErrPlanRequired) {
		t.Fatalf("got %v, want ErrPlanRequired", err)
	}
}

func TestServiceAddRejectsReservedSuffix(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(newFakeStore(), &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	_, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "demo.jesontech.com", PlanTier: "pro",
	})
	if !errors.Is(err, ErrReservedHostname) {
		t.Fatalf("got %v, want ErrReservedHostname", err)
	}
}

func TestServiceAddRejectsDuplicateHostname(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	pre := Binding{ID: "pre", Hostname: "app.example.com", Status: StatusPending, ExpiresAt: now.Add(time.Hour)}
	if err := store.Insert(context.Background(), pre); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	_, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "app.example.com", PlanTier: "pro",
	})
	if !errors.Is(err, ErrHostnameTaken) {
		t.Fatalf("got %v, want ErrHostnameTaken", err)
	}
}

func TestServiceConfirmAddInsertsBindingAndRegistersCloudflare(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cf := &fakeCloudflare{}
	auditor := &fakeAuditor{}
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(store, cf, &fakeResolver{}, auditor, fakePlanGate{allow: true}, now)
	plan, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "app.example.com", PlanTier: "pro",
	})
	if err != nil {
		t.Fatalf("plan add: %v", err)
	}
	binding, err := svc.ConfirmAdd(context.Background(), ConfirmAddInput{
		Plan: plan, PreviewID: "prev-1",
	})
	if err != nil {
		t.Fatalf("confirm add: %v", err)
	}
	if binding.CFHostnameID == "" {
		t.Fatal("expected cf hostname id populated")
	}
	if len(store.inserted) != 1 {
		t.Fatalf("inserted=%d", len(store.inserted))
	}
	events := auditor.snapshot()
	if len(events) == 0 {
		t.Fatal("expected audit event for add")
	}
}

func TestServiceConfirmAddRollsBackOnCloudflareFailure(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cf := &fakeCloudflare{
		register: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("cloudflare 503")
		},
	}
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(store, cf, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	plan, err := svc.PlanAdd(context.Background(), AddArgs{
		TeamID: "t1", ActorUserID: "u1", AppID: "app1", Hostname: "app.example.com", PlanTier: "pro",
	})
	if err != nil {
		t.Fatalf("plan add: %v", err)
	}
	_, err = svc.ConfirmAdd(context.Background(), ConfirmAddInput{Plan: plan, PreviewID: "prev-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, hit := store.byHost["app.example.com"]; hit {
		t.Fatal("expected binding row removed after Cloudflare failure")
	}
}

func TestServiceVerifyMarksBindingVerifiedAndActivates(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	pending := Binding{
		ID: "b1", TeamID: "t1", AppID: "app1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusPending, VerificationToken: "tok",
		IsApex: false, ExpiresAt: now.Add(time.Hour), CFHostnameID: "cf-1",
	}
	if err := store.Insert(context.Background(), pending); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resolver := &fakeResolver{
		cname: map[string]string{"app.example.com": "tunnel-abc.cfargotunnel.com."},
		txt:   map[string][]string{"_0ops-verify.app.example.com": {"tok"}},
	}
	activated := ""
	cf := &fakeCloudflare{activate: func(_ context.Context, id string) error {
		activated = id
		return nil
	}}
	auditor := &fakeAuditor{}
	svc := newServiceForTest(store, cf, resolver, auditor, fakePlanGate{allow: true}, now)
	out, err := svc.Verify(context.Background(), VerifyArgs{Hostname: "app.example.com"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !out.Verified {
		t.Fatal("expected verified=true")
	}
	if activated != "cf-1" {
		t.Fatalf("expected activate(cf-1), got %q", activated)
	}
	if store.byID["b1"].Status != StatusVerified {
		t.Fatalf("binding status=%s", store.byID["b1"].Status)
	}
	events := auditor.snapshot()
	if len(events) == 0 {
		t.Fatal("expected audit event")
	}
}

func TestServiceVerifyReportsTXTMissing(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	pending := Binding{
		ID: "b1", TeamID: "t1", AppID: "app1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusPending, VerificationToken: "tok",
		IsApex: false, ExpiresAt: now.Add(time.Hour), CFHostnameID: "cf-1",
	}
	_ = store.Insert(context.Background(), pending)
	resolver := &fakeResolver{
		cname: map[string]string{"app.example.com": "tunnel-abc.cfargotunnel.com."},
		txt:   map[string][]string{"_0ops-verify.app.example.com": {"wrong"}},
	}
	svc := newServiceForTest(store, &fakeCloudflare{}, resolver, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	out, err := svc.Verify(context.Background(), VerifyArgs{Hostname: "app.example.com"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.Verified {
		t.Fatal("expected verified=false")
	}
	if out.LastError == "" {
		t.Fatal("expected LastError populated")
	}
	if store.byID["b1"].Status != StatusPending {
		t.Fatalf("status should remain pending, got %s", store.byID["b1"].Status)
	}
}

func TestServiceExtendApplies(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	pending := Binding{
		ID: "b1", TeamID: "t1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusPending, ExtendsUsed: 0,
		ExpiresAt: now.Add(time.Hour),
	}
	_ = store.Insert(context.Background(), pending)
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	out, err := svc.Extend(context.Background(), "app.example.com")
	if err != nil {
		t.Fatalf("extend: %v", err)
	}
	if out.NewExtendsUsed != 1 {
		t.Fatalf("got %d, want 1", out.NewExtendsUsed)
	}
	if !out.NewExpiresAt.Equal(now.Add(time.Hour + 24*time.Hour)) {
		t.Fatalf("got %s", out.NewExpiresAt)
	}
}

func TestServiceExtendRejectsThird(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	pending := Binding{
		ID: "b1", TeamID: "t1", Hostname: "app.example.com",
		Kind: "extra", Status: StatusPending, ExtendsUsed: 2,
		ExpiresAt: now.Add(time.Hour),
	}
	_ = store.Insert(context.Background(), pending)
	svc := newServiceForTest(store, &fakeCloudflare{}, &fakeResolver{}, &fakeAuditor{}, fakePlanGate{allow: true}, now)
	_, err := svc.Extend(context.Background(), "app.example.com")
	if !errors.Is(err, ErrCannotExtend) {
		t.Fatalf("got %v, want ErrCannotExtend", err)
	}
}
