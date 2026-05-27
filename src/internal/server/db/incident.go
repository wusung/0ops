package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// IncidentRow mirrors the incident table layout introduced in migration
// 00008. Pointer fields denote columns that are nullable.
type IncidentRow struct {
	ID          string
	TeamID      string
	SubjectType string
	SubjectID   string
	Kind        string
	Severity    string
	Description *string
	TraceID     *string
	OpenedAt    time.Time
	ClosedAt    *time.Time
	ClosedBy    *string
	ClosedNote  *string
}

// IncidentInsert is the wire shape accepted by InsertIncident. Required
// fields mirror the spec § 9.1 NOT NULL columns.
type IncidentInsert struct {
	TeamID      string
	SubjectType string
	SubjectID   string
	Kind        string
	Severity    string
	Description *string
	TraceID     *string
}

// IncidentListFilter captures the read-side query parameters used by
// the HTTP / CLI surface.
type IncidentListFilter struct {
	TeamID         string
	Status         string // "open" | "closed" | "all"
	Kind           string
	Severity       string
	PageSize       int
	CursorOpenedAt *time.Time
	CursorID       string
}

// IncidentListResult bundles the page and an optional next cursor.
type IncidentListResult struct {
	Items          []IncidentRow
	NextCursor     string
	NextOpenedAt   *time.Time
	NextID         string
}

// ErrIncidentNotFound is returned when an incident lookup misses.
var ErrIncidentNotFound = errors.New("db: incident not found")

// InsertIncident persists a new incident row and returns the generated
// id. The reconciler calls this when a job flips to failed_permanently
// (spec § 9.2) and from the dashboard alert observer (`unknown` panel).
func (r *Repository) InsertIncident(ctx context.Context, in IncidentInsert) (string, time.Time, error) {
	parsedTeamID, err := parseUUID(in.TeamID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedSubjectID, err := parseUUID(in.SubjectID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse subject id: %w", err)
	}
	severity := in.Severity
	if severity == "" {
		severity = "medium"
	}
	var (
		id       pgtype.UUID
		openedAt pgtype.Timestamptz
	)
	if err := r.pool.QueryRow(ctx, `
INSERT INTO incident (team_id, subject_type, subject_id, kind, severity, description, trace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, opened_at
`, parsedTeamID, in.SubjectType, parsedSubjectID, in.Kind, severity,
		textFromPtr(in.Description), textFromPtr(in.TraceID)).Scan(&id, &openedAt); err != nil {
		return "", time.Time{}, err
	}
	return id.String(), openedAt.Time, nil
}

// GetIncident loads one incident by team-scoped id. Cross-team lookups
// (the caller's team_id does not match the row's team_id) surface as
// ErrIncidentNotFound rather than ErrForbidden — same enumeration-attack
// posture as the audit / app reads.
func (r *Repository) GetIncident(ctx context.Context, teamID, id string) (IncidentRow, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return IncidentRow{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedID, err := parseUUID(id)
	if err != nil {
		return IncidentRow{}, fmt.Errorf("parse id: %w", err)
	}
	row := IncidentRow{}
	if err := r.scanIncidentRow(r.pool.QueryRow(ctx, `
SELECT id, team_id, subject_type, subject_id, kind, severity,
       description, trace_id, opened_at, closed_at, closed_by, closed_note
FROM incident
WHERE id = $1 AND team_id = $2
`, parsedID, parsedTeamID), &row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IncidentRow{}, ErrIncidentNotFound
		}
		return IncidentRow{}, err
	}
	return row, nil
}

// ListIncidents returns one keyset-paginated page filtered by status /
// kind / severity. (opened_at, id) is the cursor tuple, descending so
// the most recent rows surface first.
func (r *Repository) ListIncidents(ctx context.Context, filter IncidentListFilter) (IncidentListResult, error) {
	parsedTeamID, err := parseUUID(filter.TeamID)
	if err != nil {
		return IncidentListResult{}, fmt.Errorf("parse team id: %w", err)
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	statusClause := ""
	switch filter.Status {
	case "", "all":
		// no filter
	case "open":
		statusClause = " AND closed_at IS NULL"
	case "closed":
		statusClause = " AND closed_at IS NOT NULL"
	default:
		return IncidentListResult{}, fmt.Errorf("invalid status filter %q", filter.Status)
	}
	kindClause := ""
	args := []any{parsedTeamID, int32(pageSize + 1)}
	if filter.Kind != "" {
		kindClause = fmt.Sprintf(" AND kind = $%d", len(args)+1)
		args = append(args, filter.Kind)
	}
	severityClause := ""
	if filter.Severity != "" {
		severityClause = fmt.Sprintf(" AND severity = $%d", len(args)+1)
		args = append(args, filter.Severity)
	}
	cursorClause := ""
	if filter.CursorOpenedAt != nil && filter.CursorID != "" {
		parsedCursorID, err := parseUUID(filter.CursorID)
		if err != nil {
			return IncidentListResult{}, fmt.Errorf("parse cursor id: %w", err)
		}
		cursorClause = fmt.Sprintf(" AND (opened_at, id) < ($%d, $%d)", len(args)+1, len(args)+2)
		args = append(args, filter.CursorOpenedAt.UTC(), parsedCursorID)
	}
	query := fmt.Sprintf(`
SELECT id, team_id, subject_type, subject_id, kind, severity,
       description, trace_id, opened_at, closed_at, closed_by, closed_note
FROM incident
WHERE team_id = $1%s%s%s%s
ORDER BY opened_at DESC, id DESC
LIMIT $2
`, statusClause, kindClause, severityClause, cursorClause)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return IncidentListResult{}, err
	}
	defer rows.Close()
	items := make([]IncidentRow, 0, pageSize)
	for rows.Next() {
		var row IncidentRow
		if err := r.scanIncidentRow(rows, &row); err != nil {
			return IncidentListResult{}, err
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return IncidentListResult{}, err
	}
	result := IncidentListResult{Items: items}
	if len(items) > pageSize {
		result.Items = items[:pageSize]
		last := result.Items[len(result.Items)-1]
		result.NextOpenedAt = &last.OpenedAt
		result.NextID = last.ID
	}
	return result, nil
}

// CloseIncident sets closed_at / closed_by / closed_note atomically. It
// is the spec § 9.3 entry point used by the CLI command — direct SQL
// closes are forbidden by spec § 16 #8.
func (r *Repository) CloseIncident(ctx context.Context, teamID, id, closedBy, note string) (IncidentRow, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return IncidentRow{}, fmt.Errorf("parse team id: %w", err)
	}
	parsedID, err := parseUUID(id)
	if err != nil {
		return IncidentRow{}, fmt.Errorf("parse id: %w", err)
	}
	parsedActor, err := parseUUID(closedBy)
	if err != nil {
		return IncidentRow{}, fmt.Errorf("parse actor id: %w", err)
	}
	var row IncidentRow
	if err := r.scanIncidentRow(r.pool.QueryRow(ctx, `
UPDATE incident
SET closed_at = COALESCE(closed_at, now()),
    closed_by = COALESCE(closed_by, $3),
    closed_note = COALESCE(closed_note, NULLIF($4, ''))
WHERE id = $1 AND team_id = $2
RETURNING id, team_id, subject_type, subject_id, kind, severity,
          description, trace_id, opened_at, closed_at, closed_by, closed_note
`, parsedID, parsedTeamID, parsedActor, note), &row); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IncidentRow{}, ErrIncidentNotFound
		}
		return IncidentRow{}, err
	}
	return row, nil
}

// CountOpenIncidents returns the current number of open incidents,
// grouped by severity. Drives the zeroops_incident_open gauge.
func (r *Repository) CountOpenIncidents(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `
SELECT severity, COUNT(*)
FROM incident
WHERE closed_at IS NULL
GROUP BY severity
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var (
			sev   string
			count int64
		)
		if err := rows.Scan(&sev, &count); err != nil {
			return nil, err
		}
		out[sev] = int(count)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func (r *Repository) scanIncidentRow(src scannable, row *IncidentRow) error {
	var (
		id, teamID, subjectID                pgtype.UUID
		closedBy                             pgtype.UUID
		subjectType, kind, severity          string
		description, traceID, closedNote     pgtype.Text
		openedAt                             pgtype.Timestamptz
		closedAt                             pgtype.Timestamptz
	)
	if err := src.Scan(&id, &teamID, &subjectType, &subjectID, &kind, &severity,
		&description, &traceID, &openedAt, &closedAt, &closedBy, &closedNote); err != nil {
		return err
	}
	row.ID = id.String()
	row.TeamID = teamID.String()
	row.SubjectType = subjectType
	row.SubjectID = subjectID.String()
	row.Kind = kind
	row.Severity = severity
	if description.Valid {
		v := description.String
		row.Description = &v
	}
	if traceID.Valid {
		v := traceID.String
		row.TraceID = &v
	}
	if openedAt.Valid {
		row.OpenedAt = openedAt.Time
	}
	if closedAt.Valid {
		v := closedAt.Time
		row.ClosedAt = &v
	}
	if closedBy.Valid {
		v := closedBy.String()
		row.ClosedBy = &v
	}
	if closedNote.Valid {
		v := closedNote.String
		row.ClosedNote = &v
	}
	return nil
}
