package reconciler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// Config bundles the runner inputs. All Interval* values default to
// the spec § 8 cadence (deploy_status 30s / argo_sync 30s / jobs 5s /
// metrics 30s) when zero.
type Config struct {
	Leader              Leader
	Store               Store
	Logger              *slog.Logger
	Observer            Observer
	Incidents           *IncidentService
	Handlers            *HandlerRegistry
	DeployStatusScanner *DeployStatusScanner
	ArgoSyncScanner     *ArgoSyncScanner
	UploadGCScanner     *UploadGCScanner

	DeployStatusInterval time.Duration
	ArgoSyncInterval     time.Duration
	JobQueueInterval     time.Duration
	MetricsInterval      time.Duration
	UploadGCInterval     time.Duration
	JobBatchSize         int
}

// Runner orchestrates the four reconciler loops. Start kicks off the
// background goroutines; Stop (via ctx cancellation) drains them.
type Runner struct {
	cfg Config
	wg  sync.WaitGroup
}

// New returns a Runner configured per cfg with sensible defaults.
func New(cfg Config) *Runner {
	if cfg.Leader == nil {
		cfg.Leader = AlwaysLeader{}
	}
	if cfg.Observer == nil {
		cfg.Observer = NopObserver()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DeployStatusInterval <= 0 {
		cfg.DeployStatusInterval = 30 * time.Second
	}
	if cfg.ArgoSyncInterval <= 0 {
		cfg.ArgoSyncInterval = 30 * time.Second
	}
	if cfg.JobQueueInterval <= 0 {
		cfg.JobQueueInterval = 5 * time.Second
	}
	if cfg.MetricsInterval <= 0 {
		cfg.MetricsInterval = 30 * time.Second
	}
	if cfg.JobBatchSize <= 0 {
		cfg.JobBatchSize = 32
	}
	if cfg.UploadGCInterval <= 0 {
		cfg.UploadGCInterval = 30 * time.Minute
	}
	return &Runner{cfg: cfg}
}

// Start launches the runner goroutines bound to ctx; the call returns
// immediately. Wait blocks until ctx is cancelled and every goroutine
// has drained.
func (r *Runner) Start(ctx context.Context) {
	if r.cfg.DeployStatusScanner != nil {
		r.spawn("deploy_status", r.cfg.DeployStatusInterval, func(ctx context.Context) {
			r.runDeployStatus(ctx)
		}, ctx)
	}
	if r.cfg.ArgoSyncScanner != nil {
		r.spawn("argo_sync", r.cfg.ArgoSyncInterval, func(ctx context.Context) {
			r.runArgoSync(ctx)
		}, ctx)
	}
	if r.cfg.Handlers != nil {
		r.spawn("job_queue", r.cfg.JobQueueInterval, func(ctx context.Context) {
			r.runJobQueue(ctx)
		}, ctx)
	}
	r.spawn("metrics", r.cfg.MetricsInterval, func(ctx context.Context) {
		r.runMetrics(ctx)
	}, ctx)
	if r.cfg.UploadGCScanner != nil {
		r.spawn("upload_gc", r.cfg.UploadGCInterval, func(ctx context.Context) {
			r.runUploadGC(ctx)
		}, ctx)
	}
}

// Wait blocks until every spawned loop has returned.
func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) spawn(name string, interval time.Duration, tick func(ctx context.Context), ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.recoverPanic(name)
		t := time.NewTicker(interval)
		defer t.Stop()
		// Fire one immediate tick so tests + dev smoke see progress
		// without waiting a full interval.
		tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick(ctx)
			}
		}
	}()
}

func (r *Runner) recoverPanic(name string) {
	if rec := recover(); rec != nil {
		// Spec § 16 #10: a goroutine panic MUST surface, not silently
		// kill the loop. With the single-replica AlwaysLeader stub we
		// log + record the failure; M5.5 will pair this with a Leader
		// release. The runtime then re-spawns on the next backend
		// restart.
		r.cfg.Logger.Error("reconciler loop panic", "loop", name, "panic", rec)
	}
}

func (r *Runner) runDeployStatus(ctx context.Context) {
	if !r.cfg.Leader.IsLeader() {
		r.cfg.Observer.ObserveTick("deploy_status", "skipped_not_leader")
		return
	}
	if _, err := r.cfg.DeployStatusScanner.Tick(ctx); err != nil {
		r.cfg.Observer.ObserveTick("deploy_status", "error")
		r.cfg.Logger.Error("deploy_status tick failed", "err", err)
		return
	}
	r.cfg.Observer.ObserveTick("deploy_status", "success")
}

func (r *Runner) runArgoSync(ctx context.Context) {
	if !r.cfg.Leader.IsLeader() {
		r.cfg.Observer.ObserveTick("argo_sync", "skipped_not_leader")
		return
	}
	if _, err := r.cfg.ArgoSyncScanner.Tick(ctx); err != nil {
		r.cfg.Observer.ObserveTick("argo_sync", "error")
		r.cfg.Logger.Error("argo_sync tick failed", "err", err)
		return
	}
	r.cfg.Observer.ObserveTick("argo_sync", "success")
}

func (r *Runner) runJobQueue(ctx context.Context) {
	if !r.cfg.Leader.IsLeader() {
		r.cfg.Observer.ObserveTick("job_queue", "skipped_not_leader")
		return
	}
	now := time.Now().UTC()
	rows, err := r.cfg.Store.ListPendingReconciliationJobs(ctx, now, r.cfg.JobBatchSize)
	if err != nil {
		r.cfg.Observer.ObserveTick("job_queue", "error")
		r.cfg.Logger.Error("list pending reconciliation_job rows failed", "err", err)
		return
	}
	for _, row := range rows {
		r.processJob(ctx, row)
	}
	r.cfg.Observer.ObserveTick("job_queue", "success")
}

// ProcessOne runs a single job through the handler registry. Exposed
// for tests that want to drive a single tick deterministically without
// the goroutine pump.
func (r *Runner) ProcessOne(ctx context.Context, row db.ReconciliationJobRow) {
	r.processJob(ctx, row)
}

func (r *Runner) processJob(ctx context.Context, row db.ReconciliationJobRow) {
	claimed, err := r.cfg.Store.ClaimReconciliationJob(ctx, row.ID)
	if err != nil {
		r.cfg.Logger.Error("claim reconciliation_job failed", "job_id", row.ID, "err", err)
		return
	}
	if !claimed {
		// Another worker (or the same loop on a re-scan) raced us.
		return
	}
	handler, ok := r.cfg.Handlers.Lookup(row.Kind)
	if !ok {
		r.reschedule(ctx, row, ErrUnknownKind)
		return
	}
	outcome := handler.Handle(ctx, row)
	switch {
	case outcome.FailedPermanently:
		r.failPermanently(ctx, row, outcome.LastError)
	case outcome.Completed:
		if err := r.cfg.Store.CompleteReconciliationJob(ctx, row.ID); err != nil {
			r.cfg.Logger.Error("complete reconciliation_job failed", "job_id", row.ID, "err", err)
			return
		}
		r.cfg.Observer.ObserveJobTerminal(row.Kind, "completed")
	default:
		r.reschedule(ctx, row, errors.New(outcomeError(outcome)))
	}
}

func outcomeError(o HandlerOutcome) string {
	if o.LastError != "" {
		return o.LastError
	}
	return "handler returned retry without error"
}

func (r *Runner) reschedule(ctx context.Context, row db.ReconciliationJobRow, cause error) {
	attempts := row.Attempts + 1
	if ShouldFailPermanently(attempts) {
		r.failPermanently(ctx, row, cause.Error())
		return
	}
	backoff := NextBackoff(row.Attempts)
	nextAt := time.Now().UTC().Add(backoff)
	if err := r.cfg.Store.RescheduleReconciliationJob(ctx, row.ID, cause.Error(), nextAt); err != nil {
		r.cfg.Logger.Error("reschedule reconciliation_job failed", "job_id", row.ID, "err", err)
	}
}

func (r *Runner) failPermanently(ctx context.Context, row db.ReconciliationJobRow, msg string) {
	if err := r.cfg.Store.FailReconciliationJobPermanently(ctx, row.ID, msg); err != nil {
		r.cfg.Logger.Error("fail reconciliation_job permanently failed", "job_id", row.ID, "err", err)
		return
	}
	r.cfg.Observer.ObserveJobTerminal(row.Kind, "failed_permanently")
	if r.cfg.Incidents != nil {
		desc := fmt.Sprintf("reconciliation_job %s (kind=%s) failed permanently: %s", row.ID, row.Kind, msg)
		traceID := ""
		if row.TraceID != nil {
			traceID = *row.TraceID
		}
		_, err := r.cfg.Incidents.Open(ctx, OpenParams{
			TeamID:      row.TeamID,
			SubjectType: row.SubjectType,
			SubjectID:   row.SubjectID,
			Kind:        IncidentKindFailedPermanently,
			Severity:    SeverityMedium,
			Description: desc,
			TraceID:     traceID,
		})
		if err != nil {
			r.cfg.Logger.Error("open incident for failed_permanently failed", "job_id", row.ID, "err", err)
		}
	}
}

func (r *Runner) runUploadGC(ctx context.Context) {
	if !r.cfg.Leader.IsLeader() {
		r.cfg.Observer.ObserveTick("upload_gc", "skipped_not_leader")
		return
	}
	processed, failed := r.cfg.UploadGCScanner.Tick(ctx)
	outcome := "ok"
	if failed > 0 {
		outcome = "partial_failure"
	}
	r.cfg.Observer.ObserveTick("upload_gc", outcome)
	r.cfg.Observer.RecordUploadGC(processed, failed)
	if processed > 0 || failed > 0 {
		r.cfg.Logger.Info("upload_gc tick complete",
			"processed", processed, "failed", failed)
	}
}

func (r *Runner) runMetrics(ctx context.Context) {
	if !r.cfg.Leader.IsLeader() {
		return
	}
	pending, err := r.cfg.Store.CountPendingReconciliationJobsByKind(ctx)
	if err == nil {
		for kind, count := range pending {
			r.cfg.Observer.SetPendingJobs(kind, count)
		}
	}
	open, err := r.cfg.Store.CountOpenIncidents(ctx)
	if err == nil {
		for sev, count := range open {
			r.cfg.Observer.SetOpenIncidents(sev, count)
		}
	}
}
