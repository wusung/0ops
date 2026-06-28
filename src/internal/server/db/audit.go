package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

// InsertAuditLog appends one row to the partitioned audit_log table, chained
// into its (team_id, partition_month) hash chain (audit-export-and-integrity
// spec § 4.4 / ADR-0015). Implements audit.Writer.
//
// The whole append runs in one transaction: lock-or-create the chain head,
// allocate the id, hash the row over its *stored* projection, INSERT with an
// explicit id / created_at / prev_hash / row_hash, then advance the head's
// tip / row_count. The row is hashed over exactly what it stores — created_at
// is generated here (UTC, truncated to microseconds, because Postgres rounds
// timestamptz) and the textual columns are stored verbatim (no NULLIF), so a
// later verify recomputes byte-identically.
func (r *Repository) InsertAuditLog(ctx context.Context, row audit.InsertRow) error {
	parsedTeamID, err := parseUUID(row.TeamID)
	if err != nil {
		return fmt.Errorf("parse team id: %w", err)
	}
	var actor any
	if row.ActorUserID != nil {
		parsed, err := parseUUID(*row.ActorUserID)
		if err != nil {
			return fmt.Errorf("parse actor id: %w", err)
		}
		actor = parsed
	}
	var subject any
	if row.SubjectID != nil {
		parsed, err := parseUUID(*row.SubjectID)
		if err != nil {
			return fmt.Errorf("parse subject id: %w", err)
		}
		subject = parsed
	}
	var preview any
	if row.PreviewID != nil {
		parsed, err := parseUUID(*row.PreviewID)
		if err != nil {
			return fmt.Errorf("parse preview id: %w", err)
		}
		preview = parsed
	}
	traceID := strings.TrimSpace(row.TraceID)
	var httpStatus any
	if row.HTTPStatus != nil {
		httpStatus = *row.HTTPStatus
	}

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	partitionMonth := audit.PartitionMonth(createdAt)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the (team, month) chain head, creating it on first write. The lock
	// serialises concurrent writers for this one chain so tip advances linearly.
	var genesis, tip []byte
	err = tx.QueryRow(ctx, `
SELECT genesis_hash, tip_hash FROM audit_chain_head
WHERE team_id = $1 AND partition_month = $2
FOR UPDATE
`, parsedTeamID, partitionMonth).Scan(&genesis, &tip)
	if errors.Is(err, pgx.ErrNoRows) {
		genesis = audit.GenesisHash(row.TeamID, partitionMonth)
		if _, err = tx.Exec(ctx, `
INSERT INTO audit_chain_head (team_id, partition_month, genesis_hash, tip_hash, row_count)
VALUES ($1, $2, $3, $3, 0)
`, parsedTeamID, partitionMonth, genesis); err != nil {
			return fmt.Errorf("init chain head: %w", err)
		}
		tip = genesis
	} else if err != nil {
		return fmt.Errorf("lock chain head: %w", err)
	}
	prev := tip

	// Allocate the id up front so it is covered by the hash.
	var id int64
	if err = tx.QueryRow(ctx,
		`SELECT nextval(pg_get_serial_sequence('audit_log', 'id'))`).Scan(&id); err != nil {
		return fmt.Errorf("allocate audit id: %w", err)
	}

	core := audit.Core{
		ID:          id,
		TeamID:      row.TeamID,
		ActorUserID: row.ActorUserID,
		Source:      row.Source,
		SubjectType: row.SubjectType,
		SubjectID:   row.SubjectID,
		Action:      row.Action,
		Args:        row.Args,
		Result:      row.Result,
		PreviewID:   row.PreviewID,
		TraceID:     traceID,
		Outcome:     row.Outcome,
		HTTPStatus:  row.HTTPStatus,
		CreatedAt:   createdAt,
	}
	canonical, err := audit.CanonicalCore(core)
	if err != nil {
		return fmt.Errorf("canonicalise audit core: %w", err)
	}
	rowHash := audit.RowHash(prev, canonical)

	if _, err = tx.Exec(ctx, `
INSERT INTO audit_log (
    id, team_id, actor_user_id, subject_type, subject_id, action,
    args, result, preview_id, trace_id, source, outcome, http_status,
    created_at, prev_hash, row_hash
)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, $12, $13, $14, $15, $16)
`,
		id,
		parsedTeamID,
		actor,
		row.SubjectType,
		subject,
		row.Action,
		[]byte(row.Args),
		[]byte(row.Result),
		preview,
		traceID,
		row.Source,
		row.Outcome,
		httpStatus,
		createdAt,
		prev,
		rowHash,
	); err != nil {
		return fmt.Errorf("insert audit_log: %w", err)
	}

	if _, err = tx.Exec(ctx, `
UPDATE audit_chain_head
SET tip_hash = $3,
    row_count = row_count + 1,
    last_row_id = $4,
    first_row_id = COALESCE(first_row_id, $4),
    updated_at = now()
WHERE team_id = $1 AND partition_month = $2
`, parsedTeamID, partitionMonth, rowHash, id); err != nil {
		return fmt.Errorf("advance chain head: %w", err)
	}

	return tx.Commit(ctx)
}

// ListAuditLog implements audit.Reader.ListAuditLog with cursor-based
// keyset pagination (created_at DESC, id DESC).
func (r *Repository) ListAuditLog(ctx context.Context, filter audit.QueryFilter) (audit.QueryResult, error) {
	parsedTeamID, err := parseUUID(filter.TeamID)
	if err != nil {
		return audit.QueryResult{}, fmt.Errorf("parse team id: %w", err)
	}

	// Build the query incrementally so optional filters do not force a
	// separate branch each.
	const baseQuery = `
SELECT a.id, a.team_id, a.actor_user_id, ua.github_login, a.source,
       COALESCE(a.subject_type, ''), a.subject_id, a.action,
       COALESCE(a.args, 'null'::jsonb), COALESCE(a.result, 'null'::jsonb),
       a.preview_id, COALESCE(a.trace_id, ''), a.outcome, a.http_status,
       a.created_at
FROM audit_log a
LEFT JOIN user_account ua ON ua.id = a.actor_user_id
WHERE a.team_id = $1
  AND a.created_at >= $2
  AND a.created_at <= $3
`
	args := []any{parsedTeamID, filter.Since.UTC(), filter.Until.UTC()}
	var conditions []string

	if filter.Scope == audit.QueryScopeSelf {
		parsedActorID, err := parseUUID(filter.ActorUserID)
		if err != nil {
			return audit.QueryResult{}, fmt.Errorf("parse actor id: %w", err)
		}
		args = append(args, parsedActorID)
		conditions = append(conditions, fmt.Sprintf("a.actor_user_id = $%d", len(args)))
	} else if strings.TrimSpace(filter.ActorUserID) != "" {
		parsedActorID, err := parseUUID(filter.ActorUserID)
		if err == nil {
			args = append(args, parsedActorID)
			conditions = append(conditions, fmt.Sprintf("a.actor_user_id = $%d", len(args)))
		} else {
			// Treat as github_login text match.
			args = append(args, filter.ActorUserID)
			conditions = append(conditions, fmt.Sprintf("ua.github_login = $%d", len(args)))
		}
	}

	if strings.TrimSpace(filter.Action) != "" {
		args = append(args, filter.Action+"%")
		conditions = append(conditions, fmt.Sprintf("a.action LIKE $%d", len(args)))
	}
	if strings.TrimSpace(filter.TraceID) != "" {
		args = append(args, filter.TraceID)
		conditions = append(conditions, fmt.Sprintf("a.trace_id = $%d", len(args)))
	}

	if filter.Cursor != "" {
		cursorTS, cursorID, err := audit.DecodeCursor(filter.Cursor)
		if err != nil {
			return audit.QueryResult{}, err
		}
		args = append(args, cursorTS.UTC())
		tsParam := len(args)
		args = append(args, cursorID)
		idParam := len(args)
		conditions = append(conditions, fmt.Sprintf("(a.created_at, a.id) < ($%d, $%d)", tsParam, idParam))
	}

	query := baseQuery
	for _, cond := range conditions {
		query += " AND " + cond + "\n"
	}
	args = append(args, filter.PageSize+1)
	query += fmt.Sprintf("ORDER BY a.created_at DESC, a.id DESC LIMIT $%d", len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return audit.QueryResult{}, err
	}
	defer rows.Close()

	items := make([]audit.Row, 0, filter.PageSize)
	for rows.Next() {
		var (
			id          int64
			teamID      pgtype.UUID
			actorID     pgtype.UUID
			actorLogin  pgtype.Text
			source      string
			subjectType string
			subjectID   pgtype.UUID
			action      string
			argsBytes   []byte
			resultBytes []byte
			previewID   pgtype.UUID
			traceID     string
			outcome     string
			httpStatus  pgtype.Int4
			createdAt   pgtype.Timestamptz
		)
		if err := rows.Scan(
			&id, &teamID, &actorID, &actorLogin, &source,
			&subjectType, &subjectID, &action,
			&argsBytes, &resultBytes, &previewID, &traceID, &outcome, &httpStatus,
			&createdAt,
		); err != nil {
			return audit.QueryResult{}, err
		}
		items = append(items, audit.Row{
			ID:          id,
			TeamID:      teamID.String(),
			ActorUserID: uuidPtr(actorID),
			ActorLogin:  textPtr(actorLogin),
			Source:      source,
			SubjectType: subjectType,
			SubjectID:   uuidPtr(subjectID),
			Action:      action,
			Args:        json.RawMessage(argsBytes),
			Result:      json.RawMessage(resultBytes),
			PreviewID:   uuidPtr(previewID),
			TraceID:     traceID,
			Outcome:     outcome,
			HTTPStatus:  intPtr(httpStatus),
			CreatedAt:   createdAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return audit.QueryResult{}, err
	}

	var nextCursor string
	if len(items) > filter.PageSize {
		last := items[filter.PageSize-1]
		nextCursor = audit.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:filter.PageSize]
	}
	return audit.QueryResult{Items: items, NextCursor: nextCursor}, nil
}

// GetAuditLogByID implements audit.Reader.GetAuditLogByID. The lookup
// is team-scoped so a cross-team id cannot leak through (spec § 6.2,
// ADR-0001 enumeration defence).
func (r *Repository) GetAuditLogByID(ctx context.Context, teamID string, id int64) (audit.Row, error) {
	parsedTeamID, err := parseUUID(teamID)
	if err != nil {
		return audit.Row{}, fmt.Errorf("parse team id: %w", err)
	}

	const query = `
SELECT a.id, a.team_id, a.actor_user_id, ua.github_login, a.source,
       COALESCE(a.subject_type, ''), a.subject_id, a.action,
       COALESCE(a.args, 'null'::jsonb), COALESCE(a.result, 'null'::jsonb),
       a.preview_id, COALESCE(a.trace_id, ''), a.outcome, a.http_status,
       a.created_at
FROM audit_log a
LEFT JOIN user_account ua ON ua.id = a.actor_user_id
WHERE a.team_id = $1 AND a.id = $2
LIMIT 1
`
	var (
		rowID       int64
		rTeamID     pgtype.UUID
		actorID     pgtype.UUID
		actorLogin  pgtype.Text
		source      string
		subjectType string
		subjectID   pgtype.UUID
		action      string
		argsBytes   []byte
		resultBytes []byte
		previewID   pgtype.UUID
		traceID     string
		outcome     string
		httpStatus  pgtype.Int4
		createdAt   pgtype.Timestamptz
	)
	err = r.pool.QueryRow(ctx, query, parsedTeamID, id).Scan(
		&rowID, &rTeamID, &actorID, &actorLogin, &source,
		&subjectType, &subjectID, &action,
		&argsBytes, &resultBytes, &previewID, &traceID, &outcome, &httpStatus,
		&createdAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return audit.Row{}, audit.ErrNotFound
		}
		return audit.Row{}, err
	}
	return audit.Row{
		ID:          rowID,
		TeamID:      rTeamID.String(),
		ActorUserID: uuidPtr(actorID),
		ActorLogin:  textPtr(actorLogin),
		Source:      source,
		SubjectType: subjectType,
		SubjectID:   uuidPtr(subjectID),
		Action:      action,
		Args:        json.RawMessage(argsBytes),
		Result:      json.RawMessage(resultBytes),
		PreviewID:   uuidPtr(previewID),
		TraceID:     traceID,
		Outcome:     outcome,
		HTTPStatus:  intPtr(httpStatus),
		CreatedAt:   createdAt.Time,
	}, nil
}

// CreateMonthlyPartition implements audit.PartitionMaintainer for the
// real Postgres backend. Idempotent: the IF NOT EXISTS keeps re-runs
// safe when two leader candidates race during failover.
func (r *Repository) CreateMonthlyPartition(ctx context.Context, month time.Time) error {
	month = monthStart(month)
	next := month.AddDate(0, 1, 0)
	tableName := "audit_log_" + audit.PartitionLabel(month)
	stmt := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF audit_log FOR VALUES FROM ('%s') TO ('%s')`,
		tableName,
		month.Format("2006-01-02"),
		next.Format("2006-01-02"),
	)
	_, err := r.pool.Exec(ctx, stmt)
	return err
}

// ArchiveDeleteAppRows moves delete_app rows from a partition into the
// permanent audit_log_archive table. Returns the number of rows moved.
func (r *Repository) ArchiveDeleteAppRows(ctx context.Context, month time.Time) (int64, error) {
	month = monthStart(month)
	next := month.AddDate(0, 1, 0)
	tag, err := r.pool.Exec(ctx, `
WITH moved AS (
    DELETE FROM audit_log
    WHERE action LIKE 'delete_app%'
      AND created_at >= $1
      AND created_at <  $2
    RETURNING id, team_id, actor_user_id, subject_type, subject_id, action,
              args, result, preview_id, trace_id, created_at, source, outcome, http_status
)
INSERT INTO audit_log_archive (
    id, team_id, actor_user_id, subject_type, subject_id, action,
    args, result, preview_id, trace_id, created_at, source, outcome, http_status
)
SELECT id, team_id, actor_user_id, subject_type, subject_id, action,
       args, result, preview_id, trace_id, created_at, source, outcome, http_status
FROM moved
ON CONFLICT (id) DO NOTHING
`, month.UTC(), next.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DropMonthlyPartition removes the named partition. The caller is
// responsible for archiving rows it wants to keep first (see
// ArchiveDeleteAppRows).
func (r *Repository) DropMonthlyPartition(ctx context.Context, month time.Time) error {
	month = monthStart(month)
	tableName := "audit_log_" + audit.PartitionLabel(month)
	_, err := r.pool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableName))
	return err
}

// ListPartitionMonths returns the months for which a partition exists.
// The catalog query relies on the naming convention from
// audit.PartitionLabel; partitions that pre-date the convention (e.g.
// the bootstrap audit_log_history table) are skipped.
func (r *Repository) ListPartitionMonths(ctx context.Context) ([]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
SELECT inhrelid::regclass::text AS partition_name
FROM pg_inherits
WHERE inhparent = 'audit_log'::regclass
ORDER BY partition_name
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]time.Time, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		const prefix = "audit_log_"
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		label := strings.TrimPrefix(name, prefix)
		if !looksLikeMonthLabel(label) {
			continue
		}
		t, err := audit.ParsePartitionLabel(label)
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func uuidPtr(v pgtype.UUID) *string {
	if !v.Valid {
		return nil
	}
	s := v.String()
	return &s
}

func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int32)
	return &i
}

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func looksLikeMonthLabel(label string) bool {
	if len(label) != 7 {
		return false
	}
	if label[4] != '_' {
		return false
	}
	for i, c := range label {
		if i == 4 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
