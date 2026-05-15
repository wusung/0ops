package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// recordingObserver captures Observer calls so tests can assert on
// the emitted metric points.
type recordingObserver struct {
	ticks           []string
	jobs            []string
	transitions     []string
	classifications []string
	openIncidents   map[string]int
	pending         map[string]int
}

func newRecordingObserver() *recordingObserver {
	return &recordingObserver{openIncidents: make(map[string]int), pending: make(map[string]int)}
}

func (r *recordingObserver) ObserveTick(kind, outcome string) {
	r.ticks = append(r.ticks, kind+":"+outcome)
}
func (r *recordingObserver) ObserveJobTerminal(kind, outcome string) {
	r.jobs = append(r.jobs, kind+":"+outcome)
}
func (r *recordingObserver) ObserveDeployTransition(from, to string) {
	r.transitions = append(r.transitions, from+"→"+to)
}
func (r *recordingObserver) ObserveFailureClassification(c string) {
	r.classifications = append(r.classifications, c)
}
func (r *recordingObserver) ObserveIncidentOpened(kind, sev string) {
	r.openIncidents[sev]++
}
func (r *recordingObserver) ObserveIncidentClosed(kind, sev string) { r.openIncidents[sev]-- }
func (r *recordingObserver) SetPendingJobs(kind string, count int)  { r.pending[kind] = count }
func (r *recordingObserver) SetOpenIncidents(string, int)           {}

func TestRunnerProcessOneCompletesHappyPath(t *testing.T) {
	store := newFakeStore()
	store.jobs = []db.ReconciliationJobRow{
		{ID: "job-1", TeamID: "team-1", SubjectType: "app", SubjectID: "00000000-0000-0000-0000-000000000001",
			Kind: "noop", Attempts: 0, Status: "pending"},
	}
	reg := NewHandlerRegistry()
	called := false
	reg.Register("noop", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome {
		called = true
		return HandlerOutcome{Completed: true}
	}))
	obs := newRecordingObserver()
	runner := New(Config{
		Store:    store,
		Handlers: reg,
		Observer: obs,
	})
	runner.ProcessOne(context.Background(), store.jobs[0])
	if !called {
		t.Fatalf("handler was not invoked")
	}
	if store.jobs[0].Status != "completed" {
		t.Fatalf("job status = %s, want completed", store.jobs[0].Status)
	}
	if len(obs.jobs) != 1 || obs.jobs[0] != "noop:completed" {
		t.Fatalf("job metric = %v, want noop:completed", obs.jobs)
	}
}

func TestRunnerProcessOneFailsPermanentlyAfterMaxAttempts(t *testing.T) {
	store := newFakeStore()
	store.jobs = []db.ReconciliationJobRow{
		{ID: "job-1", TeamID: "team-1", SubjectType: "app", SubjectID: "00000000-0000-0000-0000-000000000001",
			Kind: "always_fail", Attempts: MaxAttempts, Status: "pending"},
	}
	reg := NewHandlerRegistry()
	reg.Register("always_fail", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome {
		return HandlerOutcome{LastError: "boom"}
	}))
	obs := newRecordingObserver()
	incidents := NewIncidentService(store, nil, obs)
	runner := New(Config{
		Store:     store,
		Handlers:  reg,
		Observer:  obs,
		Incidents: incidents,
	})
	runner.ProcessOne(context.Background(), store.jobs[0])
	if store.jobs[0].Status != "failed_permanently" {
		t.Fatalf("job status = %s, want failed_permanently", store.jobs[0].Status)
	}
	if len(store.incidents) != 1 {
		t.Fatalf("expected one incident, got %d", len(store.incidents))
	}
	inc := store.incidents[0]
	if inc.Kind != string(IncidentKindFailedPermanently) || inc.Severity != string(SeverityMedium) {
		t.Fatalf("incident wrong shape: %+v", inc)
	}
	if obs.openIncidents[string(SeverityMedium)] != 1 {
		t.Fatalf("incident opened metric = %v", obs.openIncidents)
	}
}

func TestRunnerProcessOneReschedulesWhenAttemptsBelowCap(t *testing.T) {
	store := newFakeStore()
	store.jobs = []db.ReconciliationJobRow{
		{ID: "job-1", TeamID: "team-1", SubjectType: "app", SubjectID: "00000000-0000-0000-0000-000000000001",
			Kind: "always_fail", Attempts: 0, Status: "pending"},
	}
	reg := NewHandlerRegistry()
	reg.Register("always_fail", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome {
		return HandlerOutcome{LastError: "first failure"}
	}))
	runner := New(Config{Store: store, Handlers: reg})
	runner.ProcessOne(context.Background(), store.jobs[0])
	got := store.jobs[0]
	if got.Status != "pending" {
		t.Fatalf("status = %s, want pending", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if got.NextAttemptAt == nil {
		t.Fatalf("NextAttemptAt should be set")
	}
	wantMin := time.Now().Add(BaseBackoff - time.Second)
	if got.NextAttemptAt.Before(wantMin) {
		t.Fatalf("NextAttemptAt = %s, want >= %s", got.NextAttemptAt, wantMin)
	}
}

func TestRunnerProcessOneIgnoresClaimedJobs(t *testing.T) {
	store := newFakeStore()
	store.jobs = []db.ReconciliationJobRow{
		{ID: "job-1", TeamID: "team-1", SubjectType: "app", SubjectID: "00000000-0000-0000-0000-000000000001",
			Kind: "noop", Status: "in_progress"},
	}
	reg := NewHandlerRegistry()
	called := false
	reg.Register("noop", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome {
		called = true
		return HandlerOutcome{Completed: true}
	}))
	runner := New(Config{Store: store, Handlers: reg})
	runner.ProcessOne(context.Background(), store.jobs[0])
	if called {
		t.Fatalf("handler should not run for non-pending row")
	}
}

func TestRunnerProcessOneUnknownKindReschedules(t *testing.T) {
	store := newFakeStore()
	store.jobs = []db.ReconciliationJobRow{
		{ID: "job-1", TeamID: "team-1", SubjectType: "app", SubjectID: "00000000-0000-0000-0000-000000000001",
			Kind: "mystery", Attempts: 0, Status: "pending"},
	}
	reg := NewHandlerRegistry()
	runner := New(Config{Store: store, Handlers: reg})
	runner.ProcessOne(context.Background(), store.jobs[0])
	got := store.jobs[0]
	if got.Status != "pending" {
		t.Fatalf("status = %s, want pending (rescheduled)", got.Status)
	}
	if got.LastError == nil || *got.LastError == "" {
		t.Fatalf("LastError should record cause")
	}
}

func TestRunnerSkipsWhenLeaderGateFalse(t *testing.T) {
	store := newFakeStore()
	store.jobs = []db.ReconciliationJobRow{
		{ID: "job-1", TeamID: "team-1", SubjectType: "app", SubjectID: "00000000-0000-0000-0000-000000000001",
			Kind: "noop", Status: "pending"},
	}
	reg := NewHandlerRegistry()
	reg.Register("noop", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome {
		return HandlerOutcome{Completed: true}
	}))
	obs := newRecordingObserver()
	runner := New(Config{
		Leader:   stubLeader(false),
		Store:    store,
		Handlers: reg,
		Observer: obs,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // never enters the goroutine after the first tick
	runner.cfg.JobQueueInterval = 10 * time.Millisecond
	runner.runJobQueue(ctx)
	if store.jobs[0].Status != "pending" {
		t.Fatalf("status changed under follower gate: %s", store.jobs[0].Status)
	}
	if len(obs.ticks) != 1 || obs.ticks[0] != "job_queue:skipped_not_leader" {
		t.Fatalf("ticks = %v, want one skipped tick", obs.ticks)
	}
}

type stubLeader bool

func (s stubLeader) IsLeader() bool { return bool(s) }
