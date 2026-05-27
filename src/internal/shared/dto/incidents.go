package dto

import "time"

// Incident is the wire shape returned from the incidents API. Pointers
// model nullable columns (closed_at / closed_by / closed_note /
// description / trace_id). Times are RFC3339.
type Incident struct {
	ID          string     `json:"id"`
	TeamID      string     `json:"team_id"`
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	Kind        string     `json:"kind"`
	Severity    string     `json:"severity"`
	Description *string    `json:"description,omitempty"`
	TraceID     *string    `json:"trace_id,omitempty"`
	OpenedAt    time.Time  `json:"opened_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	ClosedBy    *string    `json:"closed_by,omitempty"`
	ClosedNote  *string    `json:"closed_note,omitempty"`
}

// ListIncidentsResponse is the keyset-paginated list output. NextCursor
// is nil when the caller has read every available row.
type ListIncidentsResponse struct {
	Items      []Incident `json:"items"`
	PageSize   int        `json:"page_size"`
	NextCursor *string    `json:"next_cursor,omitempty"`
}

// CloseIncidentRequest carries the optional human-readable note from
// CLI / future Web UI close operations (spec § 9.3).
type CloseIncidentRequest struct {
	Note string `json:"note,omitempty"`
}
