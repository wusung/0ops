package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuditEnqueueEvent is the projection of a freshly-inserted audit_log row that
// the transactional-outbox enqueuer needs (audit-event-notification spec
// § 7.1). All UUIDs are canonical strings; OccurredAt is the row's stored
// created_at. The enqueuer resolves team slug + actor login itself.
type AuditEnqueueEvent struct {
	AuditLogID  int64
	TeamID      string // canonical uuid
	Action      string
	Source      string
	SubjectType string
	SubjectID   *string // canonical uuid
	ActorUserID *string // canonical uuid; nil for system events
	Outcome     string
	OccurredAt  time.Time
	TraceID     string
}

// AuditEnqueuer is the transactional-outbox seam. InsertAuditLog calls it
// inside the same transaction that writes the audit_log row, after the INSERT
// and before COMMIT, so an audit row that matches a subscription always lands a
// webhook_delivery row in the same commit (hard rule #3). The implementation
// (notify.Enqueuer) lives above db; db only knows the interface, so there is no
// import cycle.
//
// Enqueue must NOT start its own transaction or commit/rollback tx — it shares
// the caller's. A returned error fails the whole audit write (caller retries).
// A pure-logic panic inside Enqueue must be isolated by the implementation
// (spec § 7.1) so it never aborts the audit commit.
type AuditEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, ev AuditEnqueueEvent) error
}

// SetAuditEnqueuer installs the outbox enqueuer. nil (the default) disables
// enqueue — audit writes behave exactly as before, which keeps existing tests
// and the migration-free path working.
func (r *Repository) SetAuditEnqueuer(e AuditEnqueuer) {
	r.auditEnqueuer = e
}
