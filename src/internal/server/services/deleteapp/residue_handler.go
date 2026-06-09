package deleteapp

import (
	"context"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/reconciler"
)

// ResidueJobKind is the reconciliation_job.kind the delete-app saga enqueues
// (execute.go) and that the reconciler runner must dispatch. Keeping the
// literal in one place prevents the producer/consumer drift that left the
// handler unregistered in the first place.
const ResidueJobKind = "cleanup_residue"

// ResidueHandler adapts HandleResidue onto the reconciler.Handler contract so
// the runner's job_queue loop dispatches cleanup_residue jobs.
//
// Without registering this handler the reconciler registry has no entry for
// the kind delete_app enqueues, so every app delete stalls in 'deleting' with
// "reconciler: unknown job kind" until the row fails permanently. Wiring it in
// cmd/server lets the saga's irreversible phase (ArgoCD prune wait → hard
// delete) actually converge.
//
// State ownership note: the runner owns the job row's terminal status (it
// calls Claim → Complete/Fail/Reschedule around Handle). HandleResidue also
// stamps attempts/completed_at via MarkReconciliationJobAttempt. On the
// success and fail paths this is benign (the runner's status write is the
// authoritative one). On the retry path attempts increments twice per tick,
// so a genuinely stuck prune fails permanently after ~4 ticks instead of 8 —
// an acceptable, fail-safe deviation that surfaces the incident sooner.
func (s *Service) ResidueHandler() reconciler.Handler {
	return reconciler.HandlerFunc(func(ctx context.Context, row db.ReconciliationJobRow) reconciler.HandlerOutcome {
		out := s.HandleResidue(ctx, residueJobFromRow(row))
		return reconciler.HandlerOutcome{
			Completed:         out.Completed,
			FailedPermanently: out.FailedPermanently,
			LastError:         out.LastError,
		}
	})
}

// residueJobFromRow projects a persisted reconciliation_job row onto the
// ResidueJob the delete-app handler consumes. AppID/AppSlug/TeamSlug come from
// the payload delete_app wrote (execute.go residuePayload); AppID falls back to
// the row's subject_id when the payload is missing it.
func residueJobFromRow(row db.ReconciliationJobRow) ResidueJob {
	appID := row.SubjectID
	if v, ok := payloadString(row.Payload, "app_id"); ok && v != "" {
		appID = v
	}
	appSlug, _ := payloadString(row.Payload, "app_slug")
	teamSlug, _ := payloadString(row.Payload, "team_slug")
	return ResidueJob{
		JobID:    row.ID,
		TeamID:   row.TeamID,
		AppID:    appID,
		AppSlug:  appSlug,
		TeamSlug: teamSlug,
		Attempts: row.Attempts,
		Payload:  row.Payload,
	}
}

func payloadString(p map[string]any, key string) (string, bool) {
	if p == nil {
		return "", false
	}
	v, ok := p[key].(string)
	return v, ok
}
