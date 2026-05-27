package deleteapp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// ResidueJob is the unit of work the cleanup_residue reconciler hands to
// HandleResidue. It mirrors the persisted reconciliation_job row plus the
// payload fields delete_app produced (spec § 6.2).
type ResidueJob struct {
	JobID    string
	TeamID   string
	AppID    string
	AppSlug  string
	TeamSlug string
	Attempts int
	Payload  map[string]any
}

// ResidueOutcome reports what the handler did. The reconciler tick uses
// the next_attempt_at hint to schedule retries.
type ResidueOutcome struct {
	Completed         bool
	NextAttemptAt     *time.Time
	FailedPermanently bool
	LastError         string
}

const (
	residueBaseBackoff = 60 * time.Second
	residueMaxBackoff  = 30 * time.Minute
	residueMaxAttempts = 8
)

// HandleResidue advances a single cleanup_residue job by one tick:
//
//  1. If the app row is already gone, mark the job completed.
//  2. If the ArgoCD Application is still present and the residue has not
//     timed out (5 min from job creation, defaulted), schedule a retry.
//  3. Otherwise hard-delete the app row and mark the job completed.
//
// On any error within the tick we record an attempt, compute exponential
// backoff (min(60s × 2^attempts, 30min)) and either schedule a retry or
// flip to failed_permanently after attempt > 8 (spec § 6.2).
//
// This handler is callable directly by tests and by the M5.3 reconciler
// scheduler tick. M5.1 does not own the leader-driven loop.
func (s *Service) HandleResidue(ctx context.Context, job ResidueJob) ResidueOutcome {
	if job.AppID == "" {
		return s.failPermanently(ctx, job, "missing app id")
	}

	// Step 1: check whether the app row is already gone — that means
	// another tick already finished the cleanup.
	if _, err := s.store.GetTeamAppBySlug(ctx, job.TeamID, job.AppSlug); err != nil {
		// Not found is the success terminal — record completion and exit.
		if isNotFound(err) {
			_ = s.store.MarkReconciliationJobAttempt(ctx, job.JobID, "", nil, true)
			return ResidueOutcome{Completed: true}
		}
		return s.recordRetry(ctx, job, fmt.Errorf("load app: %w", err))
	}

	// Step 2: ask ArgoCD whether the Application is still around. If we
	// have no checker (dev/test), assume gone after the first tick so the
	// handler is testable end-to-end without a real ArgoCD.
	stillExists := false
	if s.argoCD != nil {
		exists, err := s.argoCD.ApplicationExists(ctx, job.TeamSlug, job.AppSlug)
		if err != nil {
			return s.recordRetry(ctx, job, fmt.Errorf("argo check: %w", err))
		}
		stillExists = exists
	}

	if stillExists {
		// Treat the cleanup as a soft timeout: still pending → retry.
		return s.recordRetry(ctx, job, errors.New("argocd application still present"))
	}

	// Step 3: ArgoCD has pruned (or there's no checker). Hard delete the
	// app row; audit_log remains (no FK to app).
	if err := s.store.DeleteAppByID(ctx, job.AppID); err != nil {
		return s.recordRetry(ctx, job, fmt.Errorf("hard delete: %w", err))
	}

	previewID, _ := job.Payload["preview_id"].(string)
	traceID, _ := job.Payload["trace_id"].(string)
	appIDLocal := job.AppID
	var previewPtr, tracePtr *string
	if previewID != "" {
		previewPtr = &previewID
	}
	if traceID != "" {
		tracePtr = &traceID
	}
	_ = s.store.AppendAuditLog(ctx, auditResidueSuccess(job, appIDLocal, previewPtr, tracePtr))

	_ = s.store.MarkReconciliationJobAttempt(ctx, job.JobID, "", nil, true)
	return ResidueOutcome{Completed: true}
}

func (s *Service) recordRetry(ctx context.Context, job ResidueJob, cause error) ResidueOutcome {
	attempts := job.Attempts + 1
	if attempts > residueMaxAttempts {
		return s.failPermanently(ctx, job, cause.Error())
	}
	backoff := residueBaseBackoff * time.Duration(math.Pow(2, float64(job.Attempts)))
	if backoff > residueMaxBackoff {
		backoff = residueMaxBackoff
	}
	nextAt := s.now().Add(backoff)
	_ = s.store.MarkReconciliationJobAttempt(ctx, job.JobID, cause.Error(), &nextAt, false)
	return ResidueOutcome{NextAttemptAt: &nextAt, LastError: cause.Error()}
}

func (s *Service) failPermanently(ctx context.Context, job ResidueJob, msg string) ResidueOutcome {
	_ = s.store.MarkReconciliationJobAttempt(ctx, job.JobID, "failed_permanently: "+msg, nil, true)
	appIDLocal := job.AppID
	_ = s.store.AppendAuditLog(ctx, auditResidueFailed(job, appIDLocal, msg))
	return ResidueOutcome{FailedPermanently: true, Completed: true, LastError: msg}
}
