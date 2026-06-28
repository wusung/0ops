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

// AuditExportEntry extends a query entry with the chain linkage hashes
// (hex-encoded) so an auditor can recompute row_hash = SHA-256(prev ||
// canonical(core)) offline and confirm the chain (audit-export-and-integrity
// spec § 6.4). team_id lives once in the manifest, not per entry.
type AuditExportEntry struct {
	AuditLogEntry
	PrevHash string `json:"prev_hash,omitempty"`
	RowHash  string `json:"row_hash,omitempty"`
}

// AuditExportRange is the [since, until] window an export covers.
type AuditExportRange struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// ChainSummary is one per-(team, month) anchor in the integrity manifest.
type ChainSummary struct {
	Month       string `json:"month"` // YYYY-MM
	GenesisHash string `json:"genesis_hash"`
	TipHash     string `json:"tip_hash"`
	RowCount    int64  `json:"row_count"`
}

// IntegritySummary is the export integrity manifest (spec § 6.4, hard rule #7):
// the chain anchors touching the exported range plus provenance, the evidence
// an auditor matches a recomputed chain against. team_id is the UUID that
// genesis derivation and the per-row core both hash over.
type IntegritySummary struct {
	TeamSlug    string           `json:"team_slug"`
	TeamID      string           `json:"team_id"`
	Range       AuditExportRange `json:"range"`
	RowCount    int              `json:"row_count"`
	Chains      []ChainSummary   `json:"chains"`
	GeneratedAt time.Time        `json:"generated_at"`
	Generator   string           `json:"generator"`
}

// AuditExportEnvelope is the JSON export body (spec § 6.4): the integrity
// manifest, the exported entries, and a resume cursor when the response hit the
// per-page cap.
type AuditExportEnvelope struct {
	Manifest   IntegritySummary   `json:"manifest"`
	Entries    []AuditExportEntry `json:"entries"`
	NextCursor *string            `json:"next_cursor,omitempty"`
}
