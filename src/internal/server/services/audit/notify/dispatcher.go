package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

// breakerThreshold is the spec § 7.4 consecutive-failure count (across events)
// at which a subscription auto-disables.
const breakerThreshold = 20

// maxResponseBytes caps the receiver response read — only the status matters
// (spec § 6.4). 64 KiB.
const maxResponseBytes = 64 << 10

// AuditLogger is the narrow audit dependency the dispatcher needs to record a
// circuit-breaker disable (spec § 7.4). *audit.Service satisfies it.
type AuditLogger interface {
	Log(ctx context.Context, entry audit.Entry) error
}

// Leader gates the dispatcher to a single replica (spec § 7.2). Any type with
// IsLeader() bool works (reconciler.Leader does).
type Leader interface {
	IsLeader() bool
}

// Dispatcher is the background delivery worker. One Tick claims a batch of due
// deliveries (FOR UPDATE SKIP LOCKED), POSTs each with an HMAC signature, and
// advances the retry / circuit-breaker state machine (spec § 7.2-7.4).
type Dispatcher struct {
	pool     *pgxpool.Pool
	client   *http.Client
	audit    AuditLogger
	leader   Leader
	observer MetricObserver
	batch    int
	rnd      func() float64
	now      func() time.Time
}

// DispatcherConfig wires a Dispatcher. client / leader / observer / rnd / now
// default sensibly when nil.
type DispatcherConfig struct {
	Pool     *pgxpool.Pool
	Client   *http.Client
	Audit    AuditLogger
	Leader   Leader
	Observer MetricObserver
	Batch    int
	Rand     func() float64
	Now      func() time.Time
}

// NewDispatcher builds a Dispatcher.
func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	client := cfg.Client
	if client == nil {
		// Production default: IP-pinned, redirect-refusing client that re-checks
		// the resolved IP at dial time (delivery-side SSRF guard, spec § 6.4).
		// Tests inject their own loopback-permitting client.
		client = safeDeliveryClient()
	}
	obs := cfg.Observer
	if obs == nil {
		obs = NopObserver()
	}
	batch := cfg.Batch
	if batch <= 0 {
		batch = 50
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Dispatcher{
		pool:     cfg.Pool,
		client:   client,
		audit:    cfg.Audit,
		leader:   cfg.Leader,
		observer: obs,
		batch:    batch,
		rnd:      cfg.Rand,
		now:      now,
	}
}

// claimed is one delivery row claimed for this tick.
type claimed struct {
	id             string
	createdAt      time.Time
	subscriptionID string
	teamID         string
	event          string
	payload        []byte
	attempt        int
	maxAttempts    int
}

// Tick runs one poll+deliver cycle. It returns the number of deliveries
// processed. Non-leader replicas no-op (spec § 7.2). It never returns an error
// for an individual delivery failure — those advance the retry state machine.
func (d *Dispatcher) Tick(ctx context.Context) (int, error) {
	if d.leader != nil && !d.leader.IsLeader() {
		return 0, nil
	}
	rows, err := d.claimDue(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		d.process(ctx, row)
	}
	return len(rows), nil
}

// deliveringLease is how far claimDue pushes next_attempt_at when it flips a
// row to 'delivering'. If the worker crashes (or its terminal UPDATE is lost)
// mid-delivery, the row becomes re-claimable after the lease instead of being
// stranded forever. Redelivery of an already-sent row is harmless: the receiver
// dedups on the stable X-0ops-Delivery id.
const deliveringLease = 5 * time.Minute

// claimDue atomically claims a batch of due deliveries by flipping them to
// 'delivering' under FOR UPDATE SKIP LOCKED, so concurrent replicas never grab
// the same row (spec § 7.2). It also leases each row (next_attempt_at += lease)
// so a stale 'delivering' row (crash mid-delivery) is reclaimed later. The HTTP
// POST then happens outside any row lock.
func (d *Dispatcher) claimDue(ctx context.Context) ([]claimed, error) {
	rows, err := d.pool.Query(ctx, `
UPDATE webhook_delivery SET status = 'delivering', next_attempt_at = now() + make_interval(secs => $2)
WHERE (id, created_at) IN (
    SELECT id, created_at FROM webhook_delivery
    WHERE status IN ('pending','failed','delivering') AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    FOR UPDATE SKIP LOCKED
    LIMIT $1
)
RETURNING id, created_at, subscription_id, team_id, event, payload, attempt, max_attempts
`, d.batch, int(deliveringLease.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.id, &c.createdAt, &c.subscriptionID, &c.teamID, &c.event, &c.payload, &c.attempt, &c.maxAttempts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// process delivers one claimed row and records the outcome.
func (d *Dispatcher) process(ctx context.Context, row claimed) {
	url, key, active, err := d.loadSubscription(ctx, row.subscriptionID)
	if err != nil || !active {
		// Subscription deleted or disabled → drop (cannot deliver).
		reason := "subscription inactive"
		if err != nil {
			reason = "subscription unavailable"
		}
		d.markDropped(ctx, row, nil, 0, reason)
		return
	}

	status, ms, derr := d.deliver(ctx, row, url, key)
	if derr == nil && status >= 200 && status < 300 {
		d.markSucceeded(ctx, row, status, ms)
		return
	}
	d.markFailed(ctx, row, status, ms, derr)
}

// deliver signs and POSTs the stored payload. Success = 2xx. Returns the
// receiver status (0 on transport error) and round-trip ms.
func (d *Dispatcher) deliver(ctx context.Context, row claimed, url string, key []byte) (int, int, error) {
	ts := d.now()
	headers := SignedHeaders(key, row.event, row.id, ts, row.payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(row.payload))
	if err != nil {
		return 0, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := d.client.Do(req)
	ms := int(time.Since(start).Milliseconds())
	if err != nil {
		return 0, ms, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, ms, fmt.Errorf("receiver status %d", resp.StatusCode)
	}
	return resp.StatusCode, ms, nil
}

func (d *Dispatcher) loadSubscription(ctx context.Context, id string) (url string, key []byte, active bool, err error) {
	var b64 string
	err = d.pool.QueryRow(ctx, `SELECT url, secret_material, active FROM webhook_subscription WHERE id = $1`, id).Scan(&url, &b64, &active)
	if err != nil {
		return "", nil, false, err
	}
	// DEFERRED: secret_material is base64 plaintext in v1 (at-rest envelope
	// encryption pending secrets-management). The SecretStore seam is where the
	// decrypt step lands.
	key, err = DecodeSigningKey(b64)
	if err != nil {
		return "", nil, false, err
	}
	return url, key, active, nil
}

func (d *Dispatcher) markSucceeded(ctx context.Context, row claimed, status, ms int) {
	_, _ = d.pool.Exec(ctx, `
UPDATE webhook_delivery
SET status = 'succeeded', delivered_at = now(), attempt = attempt + 1,
    response_status = $3, response_ms = $4, error = NULL
WHERE id = $1 AND created_at = $2
`, row.id, row.createdAt, status, ms)
	_, _ = d.pool.Exec(ctx, `
UPDATE webhook_subscription SET consecutive_failures = 0, last_delivery_at = now(), updated_at = now()
WHERE id = $1
`, row.subscriptionID)
	d.observer.ObserveDelivery(row.event, "succeeded")
}

func (d *Dispatcher) markDropped(ctx context.Context, row claimed, status *int, ms int, reason string) {
	_, _ = d.pool.Exec(ctx, `
UPDATE webhook_delivery
SET status = 'dropped', delivered_at = now(), attempt = attempt + 1,
    response_status = $3, response_ms = $4, error = $5
WHERE id = $1 AND created_at = $2
`, row.id, row.createdAt, status, ms, truncErr(reason))
	d.observer.ObserveDelivery(row.event, "dropped")
}

// markFailed advances attempt; drops at max_attempts, else schedules a backoff
// retry. It bumps the subscription failure counter and trips the breaker.
func (d *Dispatcher) markFailed(ctx context.Context, row claimed, status, ms int, derr error) {
	nextAttempt := row.attempt + 1
	var statusPtr *int
	if status != 0 {
		statusPtr = &status
	}
	errText := "delivery failed"
	if derr != nil {
		errText = derr.Error()
	}

	if nextAttempt >= row.maxAttempts {
		d.markDropped(ctx, row, statusPtr, ms, errText)
	} else {
		backoff := NextBackoff(nextAttempt, d.rnd)
		next := d.now().UTC().Add(backoff)
		_, _ = d.pool.Exec(ctx, `
UPDATE webhook_delivery
SET status = 'failed', attempt = $3, next_attempt_at = $4,
    response_status = $5, response_ms = $6, error = $7
WHERE id = $1 AND created_at = $2
`, row.id, row.createdAt, nextAttempt, next, statusPtr, ms, truncErr(errText))
		d.observer.ObserveDelivery(row.event, "failed")
	}

	d.bumpFailureAndMaybeBreak(ctx, row)
}

// bumpFailureAndMaybeBreak increments the subscription's consecutive-failure
// counter and, on crossing the threshold, auto-disables it and writes ONE
// webhook_subscription_disabled audit row (spec § 7.4). The audit action is not
// catalogued (§ 5.2) so it cannot recurse into another notification.
func (d *Dispatcher) bumpFailureAndMaybeBreak(ctx context.Context, row claimed) {
	var failures int
	var active bool
	if err := d.pool.QueryRow(ctx, `
UPDATE webhook_subscription
SET consecutive_failures = consecutive_failures + 1, updated_at = now()
WHERE id = $1
RETURNING consecutive_failures, active
`, row.subscriptionID).Scan(&failures, &active); err != nil {
		return
	}
	if !active || failures < breakerThreshold {
		return
	}
	tag, err := d.pool.Exec(ctx, `
UPDATE webhook_subscription
SET active = false, disabled_reason = 'auto_circuit_breaker', updated_at = now()
WHERE id = $1 AND active = true
`, row.subscriptionID)
	if err != nil || tag.RowsAffected() == 0 {
		return // already disabled by a concurrent tick; audit only once
	}
	d.observer.ObserveCircuitBreaker()
	if d.audit != nil {
		subID := row.subscriptionID
		_ = d.audit.Log(ctx, audit.Entry{
			TeamID:      row.teamID,
			Source:      audit.SourceSystem,
			SubjectType: SubjectTypeSubscription,
			SubjectID:   &subID,
			Action:      ActionSubscriptionDisabled,
			Outcome:     audit.OutcomeSuccess,
		})
	}
}

func truncErr(s string) string {
	const max = 512
	if len(s) > max {
		return s[:max]
	}
	return s
}

// Run starts the dispatcher loop on the given interval until ctx is cancelled.
// It fires one immediate tick (dev smoke / tests see progress without waiting).
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	d.tickLogged(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.tickLogged(ctx)
		}
	}
}

func (d *Dispatcher) tickLogged(ctx context.Context) {
	if _, err := d.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		// best-effort background work; surface via metric only.
		d.observer.ObserveDelivery("", "tick_error")
	}
}
