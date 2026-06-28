package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// PreviewAction is the preview.action value for all webhook subscription writes
// (create / delete / rotate-secret). The concrete op is stored in the preview
// args so one preview-confirm pair covers every write (hard rule #9).
const PreviewAction = "webhook_subscription_write"

// maxSubscriptionsPerTeam bounds fan-out amplification (spec § 4.1 note).
const maxSubscriptionsPerTeam = 10

// Service write/lookup error sentinels mapped to apperror codes by the handler.
var (
	ErrInvalidEvents        = errors.New("notify: events invalid")
	ErrQuotaExceeded        = errors.New("notify: subscription quota exceeded")
	ErrSubscriptionNotFound = errors.New("notify: subscription not found")
	ErrDeliveryNotFound     = errors.New("notify: delivery not found")
	ErrPreviewNotFound      = errors.New("notify: preview not found")
	ErrPreviewConsumed      = errors.New("notify: preview already consumed")
	ErrPreviewExpired       = errors.New("notify: preview expired")
	ErrValidation           = errors.New("notify: validation failed")
)

// PreviewStore is the preview-row persistence the subscription writes reuse,
// satisfied by *db.Repository.
type PreviewStore interface {
	CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error)
	GetPreview(ctx context.Context, previewID string) (db.Preview, error)
	ConsumePreviewWithResult(ctx context.Context, previewID string, result json.RawMessage) error
}

// Service owns subscription CRUD + delivery queries + redeliver. It runs its
// own webhook SQL on the pool and writes audit rows for config changes (spec
// § 7.3 / § 7.6 / § 10). Delivery attempts never write audit (hard rule #5).
type Service struct {
	pool     *pgxpool.Pool
	previews PreviewStore
	audit    AuditLogger
	resolve  func(host string) ([]net.IP, error)
	now      func() time.Time
}

// NewService wires a Service. resolve is injectable for SSRF tests (nil → real
// DNS). now is injectable for preview-expiry tests (nil → time.Now).
func NewService(pool *pgxpool.Pool, previews PreviewStore, auditLog AuditLogger, resolve func(string) ([]net.IP, error), now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, previews: previews, audit: auditLog, resolve: resolve, now: now}
}

// previewArgs is the persisted shape that lets Confirm replay a write without
// re-reading client input.
type previewArgs struct {
	Op             string   `json:"op"` // create | delete | rotate
	URL            string   `json:"url,omitempty"`
	Events         []string `json:"events,omitempty"`
	Description    string   `json:"description,omitempty"`
	SubscriptionID string   `json:"subscription_id,omitempty"`
}

// PreviewResult is the handler-facing preview projection.
type PreviewResult struct {
	PreviewID   string
	Summary     string
	ExpiresAt   time.Time
	SideEffects []string
}

// PreviewCreate validates a new subscription and persists a preview. SSRF /
// scheme failures surface ErrInvalidWebhookURL → 422 (hard rule #8); an unknown
// event key surfaces ErrInvalidEvents; over-quota surfaces ErrQuotaExceeded.
func (s *Service) PreviewCreate(ctx context.Context, teamID, actorUserID string, req dto.CreateWebhookSubscriptionRequest) (PreviewResult, error) {
	if teamID == "" || actorUserID == "" {
		return PreviewResult{}, ErrValidation
	}
	if err := ValidateWebhookURL(req.URL, s.resolve); err != nil {
		return PreviewResult{}, err
	}
	events, err := normaliseEvents(req.Events)
	if err != nil {
		return PreviewResult{}, err
	}
	count, err := s.countSubscriptions(ctx, teamID)
	if err != nil {
		return PreviewResult{}, err
	}
	if count >= maxSubscriptionsPerTeam {
		return PreviewResult{}, ErrQuotaExceeded
	}
	summary := fmt.Sprintf("Create webhook subscription to %s for %d event(s)", redactHost(req.URL), len(events))
	return s.persistPreview(ctx, teamID, actorUserID, previewArgs{
		Op: "create", URL: req.URL, Events: events, Description: req.Description,
	}, summary, []string{"INSERT webhook_subscription", "Generate signing key (revealed once)"})
}

// PreviewRotate previews a signing-key rotation (write-only reveal of a new
// key on confirm).
func (s *Service) PreviewRotate(ctx context.Context, teamID, actorUserID, subscriptionID string) (PreviewResult, error) {
	if _, err := s.getSubscriptionRow(ctx, teamID, subscriptionID); err != nil {
		return PreviewResult{}, err
	}
	return s.persistPreview(ctx, teamID, actorUserID, previewArgs{Op: "rotate", SubscriptionID: subscriptionID},
		"Rotate webhook signing key (new key revealed once)", []string{"UPDATE webhook_subscription.secret_material"})
}

// PreviewDelete previews subscription deletion.
func (s *Service) PreviewDelete(ctx context.Context, teamID, actorUserID, subscriptionID string) (PreviewResult, error) {
	if _, err := s.getSubscriptionRow(ctx, teamID, subscriptionID); err != nil {
		return PreviewResult{}, err
	}
	return s.persistPreview(ctx, teamID, actorUserID, previewArgs{Op: "delete", SubscriptionID: subscriptionID},
		"Delete webhook subscription", []string{"DELETE webhook_subscription"})
}

func (s *Service) persistPreview(ctx context.Context, teamID, actorUserID string, args previewArgs, summary string, sideEffects []string) (PreviewResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return PreviewResult{}, err
	}
	row, err := s.previews.CreatePreview(ctx, teamID, actorUserID, PreviewAction, raw, summary)
	if err != nil {
		return PreviewResult{}, err
	}
	return PreviewResult{PreviewID: row.ID, Summary: summary, ExpiresAt: row.ExpiresAt, SideEffects: sideEffects}, nil
}

// Confirm consumes a preview and executes the stored op. The create / rotate
// responses carry the plaintext signing key exactly once (write-only reveal);
// the persisted last_result for idempotent replay never includes it.
func (s *Service) Confirm(ctx context.Context, teamID, actorUserID, previewID, traceID string) (dto.WebhookSubscriptionResponse, error) {
	preview, err := s.previews.GetPreview(ctx, previewID)
	if err != nil {
		if errors.Is(err, db.ErrPreviewNotFound) {
			return dto.WebhookSubscriptionResponse{}, ErrPreviewNotFound
		}
		return dto.WebhookSubscriptionResponse{}, err
	}
	if preview.Action != PreviewAction || preview.TeamID != teamID || preview.ActorUserID != actorUserID {
		return dto.WebhookSubscriptionResponse{}, ErrPreviewNotFound
	}
	if preview.ConsumedAt != nil {
		if len(preview.LastResult) == 0 {
			return dto.WebhookSubscriptionResponse{}, ErrPreviewConsumed
		}
		var resp dto.WebhookSubscriptionResponse
		if err := json.Unmarshal(preview.LastResult, &resp); err != nil {
			return dto.WebhookSubscriptionResponse{}, err
		}
		resp.TraceID = traceID
		return resp, nil // replay never re-reveals the secret
	}
	if preview.ExpiresAt.Before(s.now().UTC()) {
		return dto.WebhookSubscriptionResponse{}, ErrPreviewExpired
	}

	var args previewArgs
	if err := json.Unmarshal(preview.Args, &args); err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}

	switch args.Op {
	case "create":
		return s.execCreate(ctx, teamID, actorUserID, preview.ID, traceID, args)
	case "rotate":
		return s.execRotate(ctx, teamID, actorUserID, preview.ID, traceID, args)
	case "delete":
		return s.execDelete(ctx, teamID, actorUserID, preview.ID, traceID, args)
	default:
		return dto.WebhookSubscriptionResponse{}, ErrValidation
	}
}

func (s *Service) execCreate(ctx context.Context, teamID, actorUserID, previewID, traceID string, args previewArgs) (dto.WebhookSubscriptionResponse, error) {
	_, b64, err := GenerateSigningKey()
	if err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	var id string
	var createdAt, updatedAt time.Time
	err = s.pool.QueryRow(ctx, `
INSERT INTO webhook_subscription (team_id, url, events, secret_ref, secret_material, description, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at, updated_at
`, teamID, args.URL, args.Events, "inline", b64, nullIfEmpty(args.Description), actorUserID).Scan(&id, &createdAt, &updatedAt)
	if err != nil {
		return dto.WebhookSubscriptionResponse{}, fmt.Errorf("insert subscription: %w", err)
	}
	// Backfill secret_ref to the canonical inline form now that we have the id.
	_, _ = s.pool.Exec(ctx, `UPDATE webhook_subscription SET secret_ref = $2 WHERE id = $1`, id, "inline:"+id)

	resp := dto.WebhookSubscriptionResponse{
		ID: id, TeamSlug: "", URL: args.URL, Events: args.Events, Description: args.Description,
		Active: true, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	s.finishWrite(ctx, previewID, teamID, actorUserID, id, ActionSubscriptionCreate, resp)
	resp.Secret = b64 // write-only reveal, once
	resp.TraceID = traceID
	return resp, nil
}

func (s *Service) execRotate(ctx context.Context, teamID, actorUserID, previewID, traceID string, args previewArgs) (dto.WebhookSubscriptionResponse, error) {
	row, err := s.getSubscriptionRow(ctx, teamID, args.SubscriptionID)
	if err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	_, b64, err := GenerateSigningKey()
	if err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE webhook_subscription SET secret_material = $2, updated_at = now() WHERE id = $1`, row.ID, b64); err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	resp := row.toResponse()
	s.finishWrite(ctx, previewID, teamID, actorUserID, row.ID, ActionSecretRotate, resp)
	resp.Secret = b64
	resp.TraceID = traceID
	return resp, nil
}

func (s *Service) execDelete(ctx context.Context, teamID, actorUserID, previewID, traceID string, args previewArgs) (dto.WebhookSubscriptionResponse, error) {
	row, err := s.getSubscriptionRow(ctx, teamID, args.SubscriptionID)
	if err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM webhook_subscription WHERE id = $1 AND team_id = $2`, row.ID, teamID); err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	resp := row.toResponse()
	resp.Active = false
	s.finishWrite(ctx, previewID, teamID, actorUserID, row.ID, ActionSubscriptionDelete, resp)
	resp.TraceID = traceID
	return resp, nil
}

// finishWrite persists the (secret-free) confirm result for idempotent replay
// and writes the config-change audit row.
func (s *Service) finishWrite(ctx context.Context, previewID, teamID, actorUserID, subscriptionID, action string, resp dto.WebhookSubscriptionResponse) {
	encoded, _ := json.Marshal(resp) // resp has no Secret yet
	_ = s.previews.ConsumePreviewWithResult(ctx, previewID, encoded)
	if s.audit != nil {
		actor := actorUserID
		sub := subscriptionID
		_ = s.audit.Log(ctx, audit.Entry{
			TeamID:      teamID,
			ActorUserID: &actor,
			Source:      audit.SourceUser,
			SubjectType: SubjectTypeSubscription,
			SubjectID:   &sub,
			Action:      action,
			Outcome:     audit.OutcomeSuccess,
		})
	}
}

// List returns the team's subscriptions, never including the signing key
// (write-only reveal, hard rule #10).
func (s *Service) List(ctx context.Context, teamID string) ([]dto.WebhookSubscriptionResponse, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, url, events, description, active, disabled_reason, consecutive_failures, created_at, updated_at
FROM webhook_subscription WHERE team_id = $1 ORDER BY created_at DESC
`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dto.WebhookSubscriptionResponse
	for rows.Next() {
		r, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.toResponse())
	}
	return out, rows.Err()
}

// Get returns one subscription (secret-free).
func (s *Service) Get(ctx context.Context, teamID, subscriptionID string) (dto.WebhookSubscriptionResponse, error) {
	row, err := s.getSubscriptionRow(ctx, teamID, subscriptionID)
	if err != nil {
		return dto.WebhookSubscriptionResponse{}, err
	}
	return row.toResponse(), nil
}

// ListDeliveries returns recent delivery records for the team (admin read).
func (s *Service) ListDeliveries(ctx context.Context, teamID string, limit int) ([]dto.WebhookDeliveryResponse, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, subscription_id, event, audit_log_id, status, attempt, max_attempts,
       response_status, response_ms, error, created_at, next_attempt_at, delivered_at
FROM webhook_delivery WHERE team_id = $1 ORDER BY created_at DESC LIMIT $2
`, teamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dto.WebhookDeliveryResponse
	for rows.Next() {
		var d dto.WebhookDeliveryResponse
		var respStatus, respMS *int
		var errText *string
		var deliveredAt *time.Time
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.Event, &d.AuditID, &d.Status, &d.Attempt, &d.MaxAttempts,
			&respStatus, &respMS, &errText, &d.CreatedAt, &d.NextAttemptAt, &deliveredAt); err != nil {
			return nil, err
		}
		d.ResponseStatus = respStatus
		d.ResponseMS = respMS
		if errText != nil {
			d.Error = *errText
		}
		d.DeliveredAt = deliveredAt
		out = append(out, d)
	}
	return out, rows.Err()
}

// Redeliver clones a delivery into a fresh pending row (new delivery_id, same
// audit_log_id + subscription) and writes a webhook_redeliver audit (spec
// § 7.6). The delivery itself stays out of audit (hard rule #5).
func (s *Service) Redeliver(ctx context.Context, teamID, actorUserID, deliveryID string) (dto.RedeliverWebhookResponse, error) {
	var subID, event string
	var auditLogID int64
	var payload []byte
	err := s.pool.QueryRow(ctx, `
SELECT subscription_id, event, audit_log_id, payload
FROM webhook_delivery WHERE id = $1 AND team_id = $2
`, deliveryID, teamID).Scan(&subID, &event, &auditLogID, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dto.RedeliverWebhookResponse{}, ErrDeliveryNotFound
		}
		return dto.RedeliverWebhookResponse{}, err
	}
	var newID string
	if err := s.pool.QueryRow(ctx, `
INSERT INTO webhook_delivery (subscription_id, team_id, audit_log_id, event, payload, status)
VALUES ($1, $2, $3, $4, $5::jsonb, 'pending')
RETURNING id
`, subID, teamID, auditLogID, event, payload).Scan(&newID); err != nil {
		return dto.RedeliverWebhookResponse{}, err
	}
	if s.audit != nil {
		actor := actorUserID
		_ = s.audit.Log(ctx, audit.Entry{
			TeamID:      teamID,
			ActorUserID: &actor,
			Source:      audit.SourceUser,
			SubjectType: SubjectTypeSubscription,
			SubjectID:   &subID,
			Action:      ActionRedeliver,
			Outcome:     audit.OutcomeSuccess,
		})
	}
	return dto.RedeliverWebhookResponse{DeliveryID: newID, Status: "pending"}, nil
}

func (s *Service) countSubscriptions(ctx context.Context, teamID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_subscription WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

// subscriptionRow is the internal projection (no secret material exposed).
type subscriptionRow struct {
	ID                  string
	URL                 string
	Events              []string
	Description         *string
	Active              bool
	DisabledReason      *string
	ConsecutiveFailures int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (r subscriptionRow) toResponse() dto.WebhookSubscriptionResponse {
	resp := dto.WebhookSubscriptionResponse{
		ID: r.ID, URL: r.URL, Events: r.Events, Active: r.Active,
		ConsecutiveFailures: r.ConsecutiveFailures, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Description != nil {
		resp.Description = *r.Description
	}
	if r.DisabledReason != nil {
		resp.DisabledReason = *r.DisabledReason
	}
	return resp
}

func (s *Service) getSubscriptionRow(ctx context.Context, teamID, subscriptionID string) (subscriptionRow, error) {
	row := s.pool.QueryRow(ctx, `
SELECT id, url, events, description, active, disabled_reason, consecutive_failures, created_at, updated_at
FROM webhook_subscription WHERE id = $1 AND team_id = $2
`, subscriptionID, teamID)
	r, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return subscriptionRow{}, ErrSubscriptionNotFound
		}
		return subscriptionRow{}, err
	}
	return r, nil
}

// rowScanner abstracts pgx.Row / pgx.Rows for scanSubscription.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSubscription(row rowScanner) (subscriptionRow, error) {
	var r subscriptionRow
	if err := row.Scan(&r.ID, &r.URL, &r.Events, &r.Description, &r.Active, &r.DisabledReason, &r.ConsecutiveFailures, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return subscriptionRow{}, err
	}
	return r, nil
}

// normaliseEvents validates that every key is a catalog event and dedups.
func normaliseEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return nil, ErrInvalidEvents
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(events))
	for _, e := range events {
		e = strings.TrimSpace(e)
		if !IsCatalogEvent(e) {
			return nil, ErrInvalidEvents
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	return out, nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// redactHost returns scheme://host of a url for summaries (drops path/query).
func redactHost(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexAny(rest, "/?"); j >= 0 {
			return raw[:i+3] + rest[:j]
		}
	}
	return raw
}
