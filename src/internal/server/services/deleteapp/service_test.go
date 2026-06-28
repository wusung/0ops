package deleteapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/deleteapp"
	gitopssvc "github.com/winshare/zeroops/internal/server/services/gitops"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// --- in-memory fakes -------------------------------------------------------

type fakeStore struct {
	mu sync.Mutex

	apps             map[string]db.App     // key: teamID|slug
	appsByID         map[string]db.App     // key: appID
	previews         map[string]db.Preview // key: previewID
	previewSeq       int
	bindings         map[string][]db.AppDomainBinding  // key: appID
	inflightRuns     map[string][]db.InFlightDeployRun // key: appID
	reconciliations  []db.ReconciliationJobInsert
	auditLogs        []db.AuditLogInsert
	cancelDeployRuns []string
	updates          []statusUpdate
	deletedApps      []string

	deleteBindingsErr error
	updateStatusErr   error
	enqueueErr        error
	consumeReplayErr  error
	now               func() time.Time

	residueAttempts []residueAttempt
}

type statusUpdate struct {
	AppID  string
	Status string
}

type residueAttempt struct {
	JobID     string
	LastError string
	NextAt    *time.Time
	Completed bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		apps:            map[string]db.App{},
		appsByID:        map[string]db.App{},
		previews:        map[string]db.Preview{},
		bindings:        map[string][]db.AppDomainBinding{},
		inflightRuns:    map[string][]db.InFlightDeployRun{},
		reconciliations: []db.ReconciliationJobInsert{},
		auditLogs:       []db.AuditLogInsert{},
		now:             func() time.Time { return time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC) },
	}
}

func (s *fakeStore) putApp(app db.App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := app.TeamID + "|" + app.Slug
	s.apps[key] = app
	s.appsByID[app.ID] = app
}

func (s *fakeStore) GetTeamAppBySlug(_ context.Context, teamID, slug string) (db.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.apps[teamID+"|"+slug]
	if !ok {
		return db.App{}, pgx.ErrNoRows
	}
	return app, nil
}

func (s *fakeStore) CreatePreview(_ context.Context, teamID, actorUserID, action string, args json.RawMessage, actionSummary string) (db.Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previewSeq++
	id := fmt.Sprintf("preview-%d", s.previewSeq)
	p := db.Preview{
		ID:          id,
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        append(json.RawMessage(nil), args...),
		CreatedAt:   s.now(),
		ExpiresAt:   s.now().Add(10 * time.Minute),
	}
	s.previews[id] = p
	_ = actionSummary
	return p, nil
}

func (s *fakeStore) GetPreview(_ context.Context, previewID string) (db.Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.previews[previewID]
	if !ok {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	// deep-copy slices to avoid caller-side mutation surprises
	if len(p.Args) > 0 {
		p.Args = append(json.RawMessage(nil), p.Args...)
	}
	if len(p.LastResult) > 0 {
		p.LastResult = append(json.RawMessage(nil), p.LastResult...)
	}
	return p, nil
}

func (s *fakeStore) ConsumePreviewWithResult(_ context.Context, previewID string, result json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumeReplayErr != nil {
		return s.consumeReplayErr
	}
	p, ok := s.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	if p.ConsumedAt != nil {
		return db.ErrPreviewConsumed
	}
	now := s.now()
	p.ConsumedAt = &now
	p.LastResult = append(json.RawMessage(nil), result...)
	s.previews[previewID] = p
	return nil
}

func (s *fakeStore) ListAppDomainBindings(_ context.Context, appID string) ([]db.AppDomainBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]db.AppDomainBinding(nil), s.bindings[appID]...), nil
}

func (s *fakeStore) DeleteAppDomainBindings(_ context.Context, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteBindingsErr != nil {
		return s.deleteBindingsErr
	}
	delete(s.bindings, appID)
	return nil
}

func (s *fakeStore) ListInFlightDeployRuns(_ context.Context, appID string) ([]db.InFlightDeployRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]db.InFlightDeployRun(nil), s.inflightRuns[appID]...), nil
}

func (s *fakeStore) CancelDeployRun(_ context.Context, runID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelDeployRuns = append(s.cancelDeployRuns, runID+":"+reason)
	return nil
}

func (s *fakeStore) UpdateAppStatus(_ context.Context, appID, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateStatusErr != nil {
		return s.updateStatusErr
	}
	app, ok := s.appsByID[appID]
	if !ok {
		return pgx.ErrNoRows
	}
	app.Status = &status
	s.appsByID[appID] = app
	s.apps[app.TeamID+"|"+app.Slug] = app
	s.updates = append(s.updates, statusUpdate{AppID: appID, Status: status})
	return nil
}

func (s *fakeStore) DeleteAppByID(_ context.Context, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.appsByID[appID]
	if ok {
		delete(s.apps, app.TeamID+"|"+app.Slug)
		delete(s.appsByID, appID)
	}
	s.deletedApps = append(s.deletedApps, appID)
	return nil
}

func (s *fakeStore) EnqueueReconciliationJob(_ context.Context, in db.ReconciliationJobInsert) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enqueueErr != nil {
		return "", s.enqueueErr
	}
	s.reconciliations = append(s.reconciliations, in)
	return fmt.Sprintf("job-%d", len(s.reconciliations)), nil
}

func (s *fakeStore) AppendAuditLog(_ context.Context, in db.AuditLogInsert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLogs = append(s.auditLogs, in)
	return nil
}

func (s *fakeStore) MarkReconciliationJobAttempt(_ context.Context, jobID, lastErr string, nextAt *time.Time, completed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.residueAttempts = append(s.residueAttempts, residueAttempt{JobID: jobID, LastError: lastErr, NextAt: nextAt, Completed: completed})
	return nil
}

// --- ancillary fakes -------------------------------------------------------

type fakeCustomHostnameClient struct {
	mu      sync.Mutex
	deleted []string
	errFor  map[string]error
}

func (c *fakeCustomHostnameClient) DeleteCustomHostname(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.errFor[id]; ok {
		return err
	}
	c.deleted = append(c.deleted, id)
	return nil
}

type fakeWorkflow struct {
	canceled []int64
	err      error
}

func (f *fakeWorkflow) CancelWorkflowRun(_ context.Context, id int64) error {
	if f.err != nil {
		return f.err
	}
	f.canceled = append(f.canceled, id)
	return nil
}

type fakeGitOps struct {
	called bool
	err    error
}

func (g *fakeGitOps) RemoveAppDir(_ context.Context, _ deleteapp.GitOpsRemoveInput) (gitopssvc.GitOpsResult, error) {
	g.called = true
	if g.err != nil {
		return gitopssvc.GitOpsResult{}, g.err
	}
	return gitopssvc.GitOpsResult{GitOpsCommitSHA: "gops-deleted"}, nil
}

type fakeArgoCD struct {
	existsResponses []bool
	err             error
	idx             int
}

func (a *fakeArgoCD) ApplicationExists(_ context.Context, _, _ string) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	if a.idx >= len(a.existsResponses) {
		return false, nil
	}
	v := a.existsResponses[a.idx]
	a.idx++
	return v, nil
}

// --- helpers ---------------------------------------------------------------

func newServiceForTest(t *testing.T, store deleteapp.Store, cf deleteapp.CustomHostnameClient, wf deleteapp.WorkflowCanceler, g deleteapp.GitOpsRemover, ac deleteapp.ArgoCDChecker) *deleteapp.Service {
	t.Helper()
	svc := deleteapp.New(store, cf, wf, g, ac)
	svc = svc.WithClock(func() time.Time { return time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC) })
	return svc
}

func seedApp(s *fakeStore, teamID, appID, slug, status string) db.App {
	app := db.App{ID: appID, TeamID: teamID, Slug: slug, Status: &status}
	s.putApp(app)
	return app
}

// setPreviewRisk stamps the differentiated-confirmation metadata that the
// real db.CreatePreview computes from (action, args). The fake store does not
// recompute it, so high-risk confirm tests set it explicitly to mirror a
// production delete_app preview (critical + "DELETE <slug>").
func (s *fakeStore) setPreviewRisk(previewID, riskLevel, requiredPhrase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.previews[previewID]
	p.RiskLevel = riskLevel
	p.RequiredPhrase = requiredPhrase
	s.previews[previewID] = p
}

// --- ValidateArgs ----------------------------------------------------------

func TestValidateArgsRejectsMismatch(t *testing.T) {
	if err := deleteapp.ValidateArgs("foo", "bar"); !errors.Is(err, deleteapp.ErrConfirmMismatch) {
		t.Fatalf("expected ErrConfirmMismatch, got %v", err)
	}
}

func TestValidateArgsRejectsEmpty(t *testing.T) {
	if err := deleteapp.ValidateArgs("", "foo"); !errors.Is(err, deleteapp.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
	if err := deleteapp.ValidateArgs("foo", ""); !errors.Is(err, deleteapp.ErrValidationFailed) {
		t.Fatalf("expected ErrValidationFailed, got %v", err)
	}
}

func TestValidateArgsAcceptsExactMatch(t *testing.T) {
	if err := deleteapp.ValidateArgs("nextdemo", "nextdemo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- SideEffectsForApp -----------------------------------------------------

func TestSideEffectsContains5ItemsWithArgoCDIrreversible(t *testing.T) {
	effects := deleteapp.SideEffectsForApp(nil)
	if len(effects) != 5 {
		t.Fatalf("expected 5 side effects, got %d", len(effects))
	}
	if effects[4].Effect != "argocd_prune_resources" {
		t.Fatalf("effect 5 must be argocd prune, got %q", effects[4].Effect)
	}
	if effects[4].Reversible {
		t.Fatalf("argocd prune must be irreversible")
	}
	for i := 0; i < 4; i++ {
		if !effects[i].Reversible {
			t.Fatalf("effect %d (%s) must be reversible", i+1, effects[i].Effect)
		}
	}
}

func TestSideEffectsCountsExtraDomains(t *testing.T) {
	bindings := []db.AppDomainBinding{
		{Kind: "primary", Hostname: "a.jesontech.com"},
		{Kind: "extra", Hostname: "x.example.com"},
		{Kind: "extra", Hostname: "y.example.com"},
	}
	effects := deleteapp.SideEffectsForApp(bindings)
	if want := "Unbind 2 custom hostnames from Cloudflare"; effects[1].Description != want {
		t.Fatalf("description = %q, want %q", effects[1].Description, want)
	}
}

// --- Preview ---------------------------------------------------------------

func TestPreviewRejectsConfirmMismatch(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	svc := newServiceForTest(t, store, nil, nil, nil, nil)
	_, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "wrong")
	if !errors.Is(err, deleteapp.ErrConfirmMismatch) {
		t.Fatalf("expected ErrConfirmMismatch, got %v", err)
	}
}

func TestPreviewRejectsMissingApp(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, nil, nil)
	_, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "ghost", "ghost")
	if !errors.Is(err, deleteapp.ErrAppNotFound) {
		t.Fatalf("expected ErrAppNotFound, got %v", err)
	}
}

func TestPreviewRejectsAlreadyDeleting(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	svc := newServiceForTest(t, store, nil, nil, nil, nil)
	_, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if !errors.Is(err, deleteapp.ErrAlreadyDeleting) {
		t.Fatalf("expected ErrAlreadyDeleting, got %v", err)
	}
}

func TestPreviewSucceedsAndPersistsRow(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	store.bindings["app-1"] = []db.AppDomainBinding{{Kind: "extra", Hostname: "x.example.com"}}
	svc := newServiceForTest(t, store, nil, nil, nil, nil)
	out, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Preview.ID == "" {
		t.Fatalf("preview id missing")
	}
	if len(out.SideEffects) != 5 {
		t.Fatalf("side_effects count = %d, want 5", len(out.SideEffects))
	}
	if got := store.previews[out.Preview.ID].Action; got != deleteapp.PreviewAction {
		t.Fatalf("preview.Action = %q, want %q", got, deleteapp.PreviewAction)
	}
	found := false
	for _, a := range store.auditLogs {
		if a.Action == "delete_app_preview" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected delete_app_preview audit row")
	}
}

// --- Confirm: happy + replay -----------------------------------------------

func TestConfirmExecutesReversibleAndEnqueuesResidue(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	extraID := "cf-1"
	store.bindings["app-1"] = []db.AppDomainBinding{
		{Kind: "primary", Hostname: "nextdemo.jesontech.com"},
		{Kind: "extra", Hostname: "x.example.com", CFHostnameID: &extraID},
	}
	wf := &fakeWorkflow{}
	cf := &fakeCustomHostnameClient{}
	gops := &fakeGitOps{}
	svc := newServiceForTest(t, store, cf, wf, gops, nil)

	prev, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	res, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", "trace-xyz")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if res.Replayed {
		t.Fatalf("first confirm must not be a replay")
	}
	if res.Response.AppSlug != "nextdemo" {
		t.Fatalf("response slug = %q", res.Response.AppSlug)
	}
	if res.Response.TraceID != "trace-xyz" {
		t.Fatalf("response trace_id = %q", res.Response.TraceID)
	}
	if !cfDeleted(cf, "cf-1") {
		t.Fatalf("cloudflare DeleteCustomHostname not called for cf-1")
	}
	if !gops.called {
		t.Fatalf("gitops RemoveAppDir not called")
	}
	if got := store.appsByID["app-1"].Status; got == nil || *got != "deleting" {
		t.Fatalf("app status = %v, want deleting", got)
	}
	if len(store.reconciliations) != 1 {
		t.Fatalf("expected 1 cleanup_residue job, got %d", len(store.reconciliations))
	}
	if store.reconciliations[0].Kind != "cleanup_residue" {
		t.Fatalf("job kind = %q", store.reconciliations[0].Kind)
	}
}

func cfDeleted(cf *fakeCustomHostnameClient, id string) bool {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	for _, d := range cf.deleted {
		if d == id {
			return true
		}
	}
	return false
}

func TestConfirmIsIdempotentOnReplay(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	store.bindings["app-1"] = nil
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)

	prev, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	res1, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", "trace-1")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	res2, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", "trace-2")
	if err != nil {
		t.Fatalf("replay confirm: %v", err)
	}
	if !res2.Replayed {
		t.Fatalf("second confirm must be replay")
	}
	if res1.Response.AppID != res2.Response.AppID {
		t.Fatalf("replay returned different app id: %v vs %v", res1.Response.AppID, res2.Response.AppID)
	}
	// reversible side effects must have run exactly once
	if len(store.reconciliations) != 1 {
		t.Fatalf("cleanup_residue enqueued %d times, want 1", len(store.reconciliations))
	}
}

func TestConfirmCancelsInflightDeployRuns(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	runID := int64(98765)
	store.inflightRuns["app-1"] = []db.InFlightDeployRun{{ID: "run-1", WorkflowRunID: &runID, Status: "building"}}
	wf := &fakeWorkflow{}
	svc := newServiceForTest(t, store, nil, wf, &fakeGitOps{}, nil)

	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if _, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(wf.canceled) != 1 || wf.canceled[0] != runID {
		t.Fatalf("workflow cancel = %v, want [%d]", wf.canceled, runID)
	}
	if len(store.cancelDeployRuns) != 1 {
		t.Fatalf("expected 1 deploy_run cancel, got %d", len(store.cancelDeployRuns))
	}
}

// --- Confirm: error / boundary ---------------------------------------------

func TestConfirmRejectsWrongTeam(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	_, err := svc.Confirm(context.Background(), "team-2", "actor-1", "team-acme", prev.Preview.ID, "", "")
	if !errors.Is(err, deleteapp.ErrPreviewNotFound) {
		t.Fatalf("expected ErrPreviewNotFound for wrong team, got %v", err)
	}
}

func TestConfirmRejectsWrongActor(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	_, err := svc.Confirm(context.Background(), "team-1", "actor-other", "team-acme", prev.Preview.ID, "", "")
	if !errors.Is(err, deleteapp.ErrPreviewNotFound) {
		t.Fatalf("expected ErrPreviewNotFound for wrong actor, got %v", err)
	}
}

func TestConfirmRejectsExpiredPreview(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	// advance the service clock past expiry
	svc = svc.WithClock(func() time.Time { return time.Date(2026, 5, 16, 12, 11, 0, 0, time.UTC) })
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", "")
	if !errors.Is(err, deleteapp.ErrPreviewExpired) {
		t.Fatalf("expected ErrPreviewExpired, got %v", err)
	}
}

func TestConfirmCompensatesOnGitOpsFailure(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	store.bindings["app-1"] = nil
	gops := &fakeGitOps{err: errors.New("push conflict")}
	svc := newServiceForTest(t, store, nil, nil, gops, nil)

	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", "")
	if !errors.Is(err, deleteapp.ErrCompensateMarked) {
		t.Fatalf("expected ErrCompensateMarked, got %v", err)
	}
	if got := store.appsByID["app-1"].Status; got == nil || *got != "delete_compensated" {
		t.Fatalf("app status = %v, want delete_compensated", got)
	}
	if len(store.reconciliations) != 1 {
		t.Fatalf("expected 1 cleanup_residue job, got %d", len(store.reconciliations))
	}
	found := false
	for _, a := range store.auditLogs {
		if a.Action == "delete_app_compensated" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected delete_app_compensated audit row")
	}
}

func TestConfirmMarksResidualCFOnUnbindFailure(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	cfID1 := "cf-bad"
	cfID2 := "cf-ok"
	store.bindings["app-1"] = []db.AppDomainBinding{
		{Kind: "extra", Hostname: "x.example.com", CFHostnameID: &cfID1},
		{Kind: "extra", Hostname: "y.example.com", CFHostnameID: &cfID2},
	}
	cf := &fakeCustomHostnameClient{errFor: map[string]error{cfID1: errors.New("api timeout")}}
	svc := newServiceForTest(t, store, cf, nil, &fakeGitOps{}, nil)
	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if _, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", ""); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(store.reconciliations) != 1 {
		t.Fatalf("expected 1 cleanup_residue job, got %d", len(store.reconciliations))
	}
	residuals, _ := store.reconciliations[0].Payload["residual_cf_hostname_ids"].([]string)
	if len(residuals) != 1 || residuals[0] != cfID1 {
		t.Fatalf("residual_cf_hostname_ids = %v, want [%s]", residuals, cfID1)
	}
}

// --- Confirm: high-risk typed-confirmation gate (spec § 5.4) ---------------

func newHighRiskPreview(t *testing.T, store *fakeStore, svc *deleteapp.Service) string {
	t.Helper()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	store.bindings["app-1"] = nil
	prev, err := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	store.setPreviewRisk(prev.Preview.ID, "critical", "DELETE nextdemo")
	return prev.Preview.ID
}

func TestConfirmRejectsHighRiskWithoutPhrase(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	pid := newHighRiskPreview(t, store, svc)
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", pid, "", "trace")
	if !errors.Is(err, deleteapp.ErrConfirmationPhraseMismatch) {
		t.Fatalf("expected ErrConfirmationPhraseMismatch for empty phrase, got %v", err)
	}
	if len(store.reconciliations) != 0 {
		t.Fatalf("high-risk confirm must not execute without phrase")
	}
}

func TestConfirmRejectsHighRiskWithWrongPhrase(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	pid := newHighRiskPreview(t, store, svc)
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", pid, "delete nextdemo", "trace")
	if !errors.Is(err, deleteapp.ErrConfirmationPhraseMismatch) {
		t.Fatalf("expected ErrConfirmationPhraseMismatch for wrong phrase, got %v", err)
	}
}

func TestConfirmAcceptsHighRiskWithCorrectPhrase(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	pid := newHighRiskPreview(t, store, svc)
	res, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", pid, "DELETE nextdemo", "trace")
	if err != nil {
		t.Fatalf("confirm with correct phrase: %v", err)
	}
	if res.Response.AppSlug != "nextdemo" {
		t.Fatalf("response slug = %q", res.Response.AppSlug)
	}
}

// Phrase is an AND condition on top of preview_id: a correct phrase must NOT
// let an expired preview through (spec § 11 — phrase does not replace
// preview_id validation).
func TestConfirmHighRiskCorrectPhraseStillRejectsExpiredPreview(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	pid := newHighRiskPreview(t, store, svc)
	svc = svc.WithClock(func() time.Time { return time.Date(2026, 5, 16, 12, 11, 0, 0, time.UTC) })
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", pid, "DELETE nextdemo", "trace")
	if !errors.Is(err, deleteapp.ErrPreviewExpired) {
		t.Fatalf("expected ErrPreviewExpired even with correct phrase, got %v", err)
	}
}

// Conversely, preview_id is an AND condition on top of the phrase: a correct
// phrase against the wrong team must still be rejected as not-found.
func TestConfirmHighRiskCorrectPhraseStillRejectsWrongTeam(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	pid := newHighRiskPreview(t, store, svc)
	_, err := svc.Confirm(context.Background(), "team-2", "actor-1", "team-acme", pid, "DELETE nextdemo", "trace")
	if !errors.Is(err, deleteapp.ErrPreviewNotFound) {
		t.Fatalf("expected ErrPreviewNotFound for wrong team even with correct phrase, got %v", err)
	}
}

// Fail-closed: a high-risk preview whose required_phrase is empty (a backend
// bug / unwired catalog action) must reject even an empty confirmation_phrase.
func TestConfirmHighRiskRejectsWhenRequiredPhraseEmpty(t *testing.T) {
	store := newFakeStore()
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	pid := newHighRiskPreview(t, store, svc)
	store.setPreviewRisk(pid, "critical", "") // degenerate: high-risk, no phrase
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", pid, "", "trace")
	if !errors.Is(err, deleteapp.ErrConfirmationPhraseMismatch) {
		t.Fatalf("expected ErrConfirmationPhraseMismatch on empty required_phrase, got %v", err)
	}
}

// --- HandleResidue --------------------------------------------------------

func TestHandleResidueHardDeletesWhenArgoCDPruned(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{false}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)
	out := svc.HandleResidue(context.Background(), deleteapp.ResidueJob{
		JobID: "job-1", TeamID: "team-1", AppID: "app-1", AppSlug: "nextdemo", TeamSlug: "team-acme",
		Payload: map[string]any{"preview_id": "preview-1", "trace_id": "trace-1"},
	})
	if !out.Completed {
		t.Fatalf("expected job completed")
	}
	if len(store.deletedApps) != 1 || store.deletedApps[0] != "app-1" {
		t.Fatalf("expected app-1 hard deleted, got %v", store.deletedApps)
	}
	if len(store.residueAttempts) != 1 || !store.residueAttempts[0].Completed {
		t.Fatalf("expected one attempt marked completed, got %+v", store.residueAttempts)
	}
}

func TestHandleResidueRetriesWhileArgoCDStillExists(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{true}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)
	out := svc.HandleResidue(context.Background(), deleteapp.ResidueJob{
		JobID: "job-1", TeamID: "team-1", AppID: "app-1", AppSlug: "nextdemo", TeamSlug: "team-acme",
		Attempts: 0,
	})
	if out.Completed {
		t.Fatalf("must not complete while application still present")
	}
	if out.NextAttemptAt == nil {
		t.Fatalf("expected next_attempt_at to be scheduled")
	}
	if len(store.deletedApps) != 0 {
		t.Fatalf("must not hard-delete while application still present")
	}
}

func TestHandleResidueFailsPermanentlyAfter8Attempts(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{true}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)
	out := svc.HandleResidue(context.Background(), deleteapp.ResidueJob{
		JobID: "job-1", TeamID: "team-1", AppID: "app-1", AppSlug: "nextdemo", TeamSlug: "team-acme",
		Attempts: 8,
	})
	if !out.FailedPermanently {
		t.Fatalf("expected failed_permanently after attempts > 8")
	}
	found := false
	for _, a := range store.auditLogs {
		if a.Action == "delete_app_cleanup_residue_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected failed_permanently audit row")
	}
}

func TestHandleResidueExponentialBackoff(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "deleting")
	ac := &fakeArgoCD{existsResponses: []bool{true, true, true}}
	svc := newServiceForTest(t, store, nil, nil, nil, ac)

	clock := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	svc = svc.WithClock(func() time.Time { return clock })

	out1 := svc.HandleResidue(context.Background(), deleteapp.ResidueJob{JobID: "j", TeamID: "team-1", AppID: "app-1", AppSlug: "nextdemo", TeamSlug: "team-acme", Attempts: 0})
	out3 := svc.HandleResidue(context.Background(), deleteapp.ResidueJob{JobID: "j", TeamID: "team-1", AppID: "app-1", AppSlug: "nextdemo", TeamSlug: "team-acme", Attempts: 2})
	if out1.NextAttemptAt == nil || out3.NextAttemptAt == nil {
		t.Fatalf("missing schedule hints")
	}
	delay1 := out1.NextAttemptAt.Sub(clock)
	delay3 := out3.NextAttemptAt.Sub(clock)
	if delay1 != 60*time.Second {
		t.Fatalf("attempt0 backoff = %v, want 60s", delay1)
	}
	if delay3 != 4*60*time.Second {
		t.Fatalf("attempt2 backoff = %v, want 240s", delay3)
	}
}

// --- audit + observability invariants -------------------------------------

func TestPreviewAndConfirmEmitAuditRowsWithPreviewID(t *testing.T) {
	store := newFakeStore()
	seedApp(store, "team-1", "app-1", "nextdemo", "live")
	svc := newServiceForTest(t, store, nil, nil, &fakeGitOps{}, nil)
	prev, _ := svc.Preview(context.Background(), "team-1", "actor-1", "team-acme", "nextdemo", "nextdemo")
	_, err := svc.Confirm(context.Background(), "team-1", "actor-1", "team-acme", prev.Preview.ID, "", "trace-x")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	previewSeen, confirmSeen := false, false
	for _, a := range store.auditLogs {
		switch a.Action {
		case "delete_app_preview":
			previewSeen = true
			if a.PreviewID == nil || *a.PreviewID != prev.Preview.ID {
				t.Fatalf("preview audit missing preview_id")
			}
		case "delete_app_confirm":
			confirmSeen = true
			if a.TraceID == nil || *a.TraceID != "trace-x" {
				t.Fatalf("confirm audit missing trace_id")
			}
		}
	}
	if !previewSeen || !confirmSeen {
		t.Fatalf("expected preview+confirm audit rows, got %+v", store.auditLogs)
	}
}

// --- DTO contract ---------------------------------------------------------

func TestAppDeleteResponseRoundTripsJSON(t *testing.T) {
	want := dto.AppDeleteResponse{AppID: "a", AppSlug: "s", DeletedAt: "2026-05-16T12:00:00Z", TraceID: "t"}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got dto.AppDeleteResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, want)
	}
}
