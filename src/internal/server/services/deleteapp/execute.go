package deleteapp

import (
	"context"
	"fmt"

	"github.com/winshare/zeroops/internal/server/db"
)

// executeContext captures the inputs needed by every reversible step.
type executeContext struct {
	teamID      string
	teamSlug    string
	appID       string
	appSlug     string
	previewID   string
	traceID     string
	actorUserID string
}

// executeReversible runs R1-R4 in order. On any error before R4 it walks
// back compensating committed steps (per spec § 5.2 Compensate), marks
// the app delete_compensated, and enqueues cleanup_residue so an operator
// or the reconciler can finish the job.
//
// On success it sets app.status='deleting' and enqueues cleanup_residue
// to drive the irreversible phase (ArgoCD prune wait + hard delete).
func (s *Service) executeReversible(ctx context.Context, ec executeContext) error {
	completed := make([]string, 0, 4)
	residuePayload := map[string]any{
		"app_id":     ec.appID,
		"app_slug":   ec.appSlug,
		"team_slug":  ec.teamSlug,
		"preview_id": ec.previewID,
		"trace_id":   ec.traceID,
	}

	// R1: cancel in-flight deploy_run. Best-effort: a single failure
	// does NOT abort the saga, but we record it and let cleanup_residue
	// retry the GHA cancel if anything was missed.
	inflight, err := s.store.ListInFlightDeployRuns(ctx, ec.appID)
	if err != nil {
		return s.compensate(ctx, ec, completed, residuePayload, fmt.Errorf("list inflight runs: %w", err))
	}
	cancelFailures := make([]string, 0)
	for _, dr := range inflight {
		if dr.WorkflowRunID != nil && s.workflow != nil {
			if cerr := s.workflow.CancelWorkflowRun(ctx, *dr.WorkflowRunID); cerr != nil {
				cancelFailures = append(cancelFailures, dr.ID)
			}
		}
		if cerr := s.store.CancelDeployRun(ctx, dr.ID, "delete_app_initiated"); cerr != nil {
			cancelFailures = append(cancelFailures, dr.ID)
		}
	}
	if len(cancelFailures) > 0 {
		residuePayload["cancel_failed_runs"] = cancelFailures
	}
	completed = append(completed, "cancel_inflight_deploys")

	// R2: release Cloudflare custom hostnames. A partial failure here
	// cannot be auto-recovered (spec § 5.2 Compensate); we instead carry
	// the still-bound CF hostname ids into the residue payload.
	bindings, err := s.store.ListAppDomainBindings(ctx, ec.appID)
	if err != nil {
		return s.compensate(ctx, ec, completed, residuePayload, fmt.Errorf("list bindings: %w", err))
	}
	residualCFIDs := make([]string, 0)
	for _, b := range bindings {
		if b.Kind != "extra" || b.CFHostnameID == nil || *b.CFHostnameID == "" {
			continue
		}
		if s.cf == nil {
			residualCFIDs = append(residualCFIDs, *b.CFHostnameID)
			continue
		}
		if cerr := s.cf.DeleteCustomHostname(ctx, *b.CFHostnameID); cerr != nil {
			residualCFIDs = append(residualCFIDs, *b.CFHostnameID)
		}
	}
	if len(residualCFIDs) > 0 {
		residuePayload["residual_cf_hostname_ids"] = residualCFIDs
	}
	completed = append(completed, "unbind_custom_hostnames")

	// R3: delete domain_binding rows. This is a single SQL DELETE; any
	// error here is treated as hard-failure and compensated.
	if err := s.store.DeleteAppDomainBindings(ctx, ec.appID); err != nil {
		return s.compensate(ctx, ec, completed, residuePayload, fmt.Errorf("delete bindings: %w", err))
	}
	completed = append(completed, "delete_domain_bindings")

	// R4: render & push gitops manifest removal. The gitops dependency
	// is optional in dev — when missing, the residue handler will retry.
	if s.gitops != nil {
		if _, err := s.gitops.RemoveAppDir(ctx, GitOpsRemoveInput{
			TeamSlug:    ec.teamSlug,
			AppSlug:     ec.appSlug,
			PreviewID:   ec.previewID,
			TraceID:     ec.traceID,
			DeleteRunID: ec.previewID,
		}); err != nil {
			return s.compensate(ctx, ec, completed, residuePayload, fmt.Errorf("gitops remove: %w", err))
		}
	} else {
		residuePayload["gitops_remove_deferred"] = true
	}
	completed = append(completed, "remove_gitops_manifest")

	// Set app.status='deleting'. From this point on the saga commits and
	// the irreversible phase is driven by cleanup_residue.
	if err := s.store.UpdateAppStatus(ctx, ec.appID, "deleting"); err != nil {
		return s.compensate(ctx, ec, completed, residuePayload, fmt.Errorf("set deleting: %w", err))
	}

	// Always enqueue cleanup_residue on the success path so the reconciler
	// can carry the row through ArgoCD prune wait → hard delete.
	if _, err := s.store.EnqueueReconciliationJob(ctx, db.ReconciliationJobInsert{
		TeamID:      ec.teamID,
		SubjectType: SubjectType,
		SubjectID:   ec.appID,
		Kind:        "cleanup_residue",
		Payload:     residuePayload,
	}); err != nil {
		// Non-fatal: the row is already 'deleting'. Audit the failure;
		// an operator can re-enqueue manually.
		appIDLocal := ec.appID
		previewIDLocal := ec.previewID
		_ = s.store.AppendAuditLog(ctx, db.AuditLogInsert{
			TeamID:      ec.teamID,
			ActorUserID: ptr(ec.actorUserID),
			SubjectType: SubjectType,
			SubjectID:   &appIDLocal,
			Action:      "delete_app_enqueue_residue_failed",
			Args:        residuePayload,
			Result:      map[string]any{"error": err.Error()},
			PreviewID:   &previewIDLocal,
		})
	}
	return nil
}

// compensate walks back the committed reversible steps in reverse order
// per spec § 5.2 Compensate, marks the app delete_compensated, and
// enqueues cleanup_residue so the reconciler (or an operator) can finish
// the job. The original error is returned wrapped in ErrCompensateMarked.
func (s *Service) compensate(ctx context.Context, ec executeContext, completed []string, residue map[string]any, cause error) error {
	residue["compensate_cause"] = cause.Error()
	residue["compensate_steps"] = completed

	// R4 revert is out of scope for v1 — git revert/push is heavyweight
	// and cleanup_residue can retry the removal. Mark for operator review.
	for i := len(completed) - 1; i >= 0; i-- {
		switch completed[i] {
		case "remove_gitops_manifest":
			residue["gitops_revert_required"] = true
		case "delete_domain_bindings":
			residue["domain_bindings_restore_required"] = true
		case "unbind_custom_hostnames":
			// Spec § 5.2: v1 不自動恢復；標 cleanup_residue.
			residue["unbind_rebind_required"] = false
		case "cancel_inflight_deploys":
			// Spec § 5.2: 不重啟；audit only.
			residue["inflight_restart_required"] = false
		}
	}

	// Mark app delete_compensated. Failure here is logged but does not
	// shadow the original cause.
	if updErr := s.store.UpdateAppStatus(ctx, ec.appID, "delete_compensated"); updErr != nil {
		residue["status_update_error"] = updErr.Error()
	}
	if _, jobErr := s.store.EnqueueReconciliationJob(ctx, db.ReconciliationJobInsert{
		TeamID:      ec.teamID,
		SubjectType: SubjectType,
		SubjectID:   ec.appID,
		Kind:        "cleanup_residue",
		Payload:     residue,
	}); jobErr != nil {
		residue["enqueue_error"] = jobErr.Error()
	}

	appIDLocal := ec.appID
	previewIDLocal := ec.previewID
	traceIDLocal := ec.traceID
	_ = s.store.AppendAuditLog(ctx, db.AuditLogInsert{
		TeamID:      ec.teamID,
		ActorUserID: ptr(ec.actorUserID),
		SubjectType: SubjectType,
		SubjectID:   &appIDLocal,
		Action:      "delete_app_compensated",
		Args:        residue,
		Result:      map[string]any{"status": "delete_compensated"},
		PreviewID:   &previewIDLocal,
		TraceID:     &traceIDLocal,
	})

	return fmt.Errorf("%w: %v", ErrCompensateMarked, cause)
}
