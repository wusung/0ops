package dto

import "time"

// NotificationPayload is the outbound webhook body for an audit event
// (audit-event-notification spec § 6.2). It is deliberately a redacted,
// whitelist-only projection: it never carries audit args / result, and never a
// secret / token / signature field (spec § 17 hard rule #2). The same byte
// snapshot is stored on webhook_delivery.payload and POSTed verbatim on every
// retry so the receiver can dedup on delivery_id.
type NotificationPayload struct {
	DeliveryID  string    `json:"delivery_id"`
	Event       string    `json:"event"`
	TeamSlug    string    `json:"team_slug"`
	OccurredAt  time.Time `json:"occurred_at"`
	Actor       *string   `json:"actor,omitempty"`
	Source      string    `json:"source"`
	SubjectType string    `json:"subject_type"`
	SubjectID   *string   `json:"subject_id,omitempty"`
	Outcome     string    `json:"outcome"`
	AuditID     int64     `json:"audit_id"`
	TraceID     *string   `json:"trace_id,omitempty"`
	Summary     string    `json:"summary"`
}

// CreateWebhookSubscriptionRequest is the preview payload for creating a
// per-team subscription (spec § 6.1). events holds notify event keys.
type CreateWebhookSubscriptionRequest struct {
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Description string   `json:"description,omitempty"`
}

// ConfirmWebhookSubscriptionRequest confirms a create/update/delete preview.
type ConfirmWebhookSubscriptionRequest struct {
	PreviewID string `json:"preview_id"`
}

// WebhookSubscriptionResponse is the subscription projection returned by the
// API. It never carries the signing key except on the create / rotate response
// via the separate Secret field (write-only reveal, spec § 10).
type WebhookSubscriptionResponse struct {
	ID                  string    `json:"id"`
	TeamSlug            string    `json:"team_slug"`
	URL                 string    `json:"url"`
	Events              []string  `json:"events"`
	Description         string    `json:"description,omitempty"`
	Active              bool      `json:"active"`
	DisabledReason      string    `json:"disabled_reason,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	// Secret is the plaintext signing key, populated ONLY on the create and
	// rotate-secret responses (write-only reveal). Any GET path leaves it empty.
	Secret string `json:"secret,omitempty"`
	// TraceID echoes the request trace for the confirm response.
	TraceID string `json:"trace_id,omitempty"`
}

// ListWebhookSubscriptionsResponse wraps a page of subscriptions.
type ListWebhookSubscriptionsResponse struct {
	Items []WebhookSubscriptionResponse `json:"items"`
}

// WebhookDeliveryResponse is one delivery record (spec § 6.1 query API).
type WebhookDeliveryResponse struct {
	ID             string     `json:"id"`
	SubscriptionID string     `json:"subscription_id"`
	Event          string     `json:"event"`
	AuditID        int64      `json:"audit_id"`
	Status         string     `json:"status"`
	Attempt        int        `json:"attempt"`
	MaxAttempts    int        `json:"max_attempts"`
	ResponseStatus *int       `json:"response_status,omitempty"`
	ResponseMS     *int       `json:"response_ms,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	NextAttemptAt  time.Time  `json:"next_attempt_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

// ListWebhookDeliveriesResponse wraps a page of delivery records.
type ListWebhookDeliveriesResponse struct {
	Items []WebhookDeliveryResponse `json:"items"`
}

// RedeliverWebhookResponse is returned by the manual redeliver endpoint.
type RedeliverWebhookResponse struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"`
	TraceID    string `json:"trace_id,omitempty"`
}
