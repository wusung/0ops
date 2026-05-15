package reconciler

import (
	"context"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// Store is the persistence boundary the reconciler service depends on.
// Production wires the package-local *db.Repository; tests substitute
// an in-memory fake so the scheduler and lifecycle logic can be driven
// deterministically.
type Store interface {
	// Reconciliation job queue
	ListPendingReconciliationJobs(ctx context.Context, now time.Time, limit int) ([]db.ReconciliationJobRow, error)
	CountPendingReconciliationJobsByKind(ctx context.Context) (map[string]int, error)
	ClaimReconciliationJob(ctx context.Context, jobID string) (bool, error)
	CompleteReconciliationJob(ctx context.Context, jobID string) error
	RescheduleReconciliationJob(ctx context.Context, jobID string, lastErr string, nextAt time.Time) error
	FailReconciliationJobPermanently(ctx context.Context, jobID string, lastErr string) error

	// Stuck deploy_run scanners
	ListStuckBuildingDeployRuns(ctx context.Context, threshold time.Time, limit int) ([]db.StuckDeployRun, error)
	ListStuckSyncingDeployRuns(ctx context.Context, threshold time.Time, limit int) ([]db.StuckDeployRun, error)

	// Deploy_run mutation
	TransitionDeployRun(ctx context.Context, params db.DeployRunTransitionParams) error

	// Incident lifecycle
	InsertIncident(ctx context.Context, in db.IncidentInsert) (string, time.Time, error)
	GetIncident(ctx context.Context, teamID, id string) (db.IncidentRow, error)
	ListIncidents(ctx context.Context, filter db.IncidentListFilter) (db.IncidentListResult, error)
	CloseIncident(ctx context.Context, teamID, id, closedBy, note string) (db.IncidentRow, error)
	CountOpenIncidents(ctx context.Context) (map[string]int, error)
}
