package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
)

// Enqueuer implements db.AuditEnqueuer. It runs inside the audit insert
// transaction (spec § 7.1): catalog-match the action, fan-out to the active
// subscriptions that subscribe the event, and INSERT one webhook_delivery per
// hit. It performs no HTTP and never blocks the business flow.
type Enqueuer struct {
	observer MetricObserver
	// onBeforeInsert is a test-only seam to inject a pure-logic panic between
	// subscription resolution and the delivery INSERT, exercising the spec § 7.1
	// panic-isolation guarantee. Always nil in production.
	onBeforeInsert func()
}

// NewEnqueuer builds an Enqueuer. observer may be nil (→ NopObserver).
func NewEnqueuer(observer MetricObserver) *Enqueuer {
	if observer == nil {
		observer = NopObserver()
	}
	return &Enqueuer{observer: observer}
}

// Enqueue satisfies db.AuditEnqueuer. The whole body is panic-isolated: a
// pure-logic panic records a metric and returns nil so it never aborts the
// audit commit (spec § 7.1 — notify is best-effort, audit is the hard
// guarantee). A real DB error propagates and fails the shared tx.
func (e *Enqueuer) Enqueue(ctx context.Context, tx pgx.Tx, ev db.AuditEnqueueEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			e.observer.ObserveEnqueue("panic_isolated")
			err = nil
		}
	}()

	eventKey, summarise, ok := Lookup(ev.Action)
	if !ok {
		e.observer.ObserveEnqueue("skipped")
		return nil
	}

	teamSlug, err := scanString(ctx, tx, `SELECT slug FROM team WHERE id = $1`, ev.TeamID)
	if err != nil {
		return fmt.Errorf("resolve team slug: %w", err)
	}

	var actorLogin *string
	if ev.ActorUserID != nil {
		login, err := scanNullableString(ctx, tx, `SELECT github_login FROM user_account WHERE id = $1`, *ev.ActorUserID)
		if err != nil {
			return fmt.Errorf("resolve actor login: %w", err)
		}
		actorLogin = login
	}

	rows, err := tx.Query(ctx, `
SELECT id FROM webhook_subscription
WHERE team_id = $1 AND active = true AND $2 = ANY(events)
`, ev.TeamID, string(eventKey))
	if err != nil {
		return fmt.Errorf("select subscriptions: %w", err)
	}
	var subscriptionIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan subscription id: %w", err)
		}
		subscriptionIDs = append(subscriptionIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate subscriptions: %w", err)
	}
	if len(subscriptionIDs) == 0 {
		e.observer.ObserveEnqueue("skipped")
		return nil
	}

	notifyEvent := NotifyEvent{
		AuditLogID:  ev.AuditLogID,
		TeamID:      ev.TeamID,
		TeamSlug:    teamSlug,
		Action:      ev.Action,
		Source:      ev.Source,
		SubjectType: ev.SubjectType,
		SubjectID:   ev.SubjectID,
		ActorLogin:  actorLogin,
		Outcome:     ev.Outcome,
		OccurredAt:  ev.OccurredAt,
		TraceID:     traceIDPtr(ev.TraceID),
	}
	summary := summarise(notifyEvent)

	if e.onBeforeInsert != nil {
		e.onBeforeInsert()
	}

	for _, subID := range subscriptionIDs {
		deliveryID := uuid.NewString()
		payload := BuildPayload(deliveryID, eventKey, summary, notifyEvent)
		body, err := MarshalPayload(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		// Runtime guard: never persist a snapshot carrying a forbidden field
		// (hard rule #2). Fail closed rather than store a leak.
		if !AssertNoForbiddenKeys(body) {
			return errors.New("notify: payload tripped forbidden-key guard")
		}

		if err := insertDelivery(ctx, tx, deliveryID, subID, ev, string(eventKey), body); err != nil {
			return err
		}
	}
	e.observer.ObserveEnqueue("enqueued")
	return nil
}

// insertDelivery writes one pending delivery row, idempotently. ON CONFLICT DO
// NOTHING (no target) is used rather than catching the unique_violation in Go:
// a raised 23505 would abort the SHARED audit transaction and roll back the
// audit row, breaking hard rule #3. ON CONFLICT DO NOTHING (supported on a
// partitioned table against the per-partition dedup index) keeps the tx alive.
//
// created_at is pinned to the audit event time (not now()) so an enqueue replay
// of the same audit row produces an identical (subscription, audit_log_id,
// created_at) tuple and is deduped, while a manual redeliver (created_at =
// now()) is a distinct row and is allowed (spec § 4.2 / § 7.6, hard rule #7).
func insertDelivery(ctx context.Context, tx pgx.Tx, deliveryID, subscriptionID string, ev db.AuditEnqueueEvent, event string, body []byte) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO webhook_delivery
    (id, subscription_id, team_id, audit_log_id, event, payload, status, created_at, next_attempt_at)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'pending', $7, $7)
ON CONFLICT DO NOTHING
`, deliveryID, subscriptionID, ev.TeamID, ev.AuditLogID, event, body, ev.OccurredAt); err != nil {
		return fmt.Errorf("insert delivery: %w", err)
	}
	return nil
}

func scanString(ctx context.Context, tx pgx.Tx, query, arg string) (string, error) {
	var out string
	if err := tx.QueryRow(ctx, query, arg).Scan(&out); err != nil {
		return "", err
	}
	return out, nil
}

func scanNullableString(ctx context.Context, tx pgx.Tx, query, arg string) (*string, error) {
	var out *string
	if err := tx.QueryRow(ctx, query, arg).Scan(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func traceIDPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
