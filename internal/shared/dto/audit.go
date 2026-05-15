package dto

import (
	"encoding/json"
	"time"
)

// AuditLogEntry is the wire DTO for one audit_log row returned by the
// query API (spec § 6.3). Args / result are post-redaction; the
// backend never returns raw secrets here.
type AuditLogEntry struct {
	ID          int64           `json:"id"`
	Time        time.Time       `json:"time"`
	Source      string          `json:"source"`
	Actor       *string         `json:"actor,omitempty"`
	ActorUserID *string         `json:"actor_user_id,omitempty"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type,omitempty"`
	SubjectID   *string         `json:"subject_id,omitempty"`
	Outcome     string          `json:"outcome"`
	PreviewID   *string         `json:"preview_id,omitempty"`
	TraceID     *string         `json:"trace_id,omitempty"`
	HTTPStatus  *int            `json:"http_status,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// ListAuditResponse wraps a page of audit_log rows.
type ListAuditResponse struct {
	Items      []AuditLogEntry `json:"items"`
	NextCursor *string         `json:"next_cursor,omitempty"`
	PageSize   int             `json:"page_size"`
}
