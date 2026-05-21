package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Upload represents a row in app_source_uploads (ADR-0013 §3.3 / spec §10).
type Upload struct {
	ID            string
	TeamID        string
	ActorUserID   string
	SizeBytes     int64
	SHA256        string
	ArchiveFormat string
	Status        string
	PinnedAt      *time.Time
	ExpiresAt     time.Time
	ReceivedAt    time.Time
	GCAt          *time.Time
}

// ErrUploadNotFound is returned by upload queries when a row is missing,
// belongs to a different team, or is not in the expected status. Callers
// must NOT distinguish these reasons (cross-team must look identical to
// not-found to prevent existence-disclosure side channels).
var ErrUploadNotFound = errors.New("upload not found")

// InsertUpload writes a freshly-received upload row. Caller supplies all
// fields; the server computes id/sha256/size_bytes before calling.
func (r *Repository) InsertUpload(ctx context.Context, u Upload) error {
	_, err := r.pool.Exec(ctx, `
INSERT INTO app_source_uploads
    (id, team_id, actor_user_id, size_bytes, sha256, archive_format, status, expires_at)
VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)`,
		u.ID, u.TeamID, u.ActorUserID, u.SizeBytes, u.SHA256, u.ArchiveFormat, u.Status, u.ExpiresAt)
	return err
}

// GetUpload returns the row for (team_id, id). Returns ErrUploadNotFound
// if absent, scoped to a different team, or otherwise invisible.
func (r *Repository) GetUpload(ctx context.Context, teamID, id string) (Upload, error) {
	var u Upload
	var pinnedAt, gcAt *time.Time
	err := r.pool.QueryRow(ctx, `
SELECT id, team_id::text, actor_user_id::text, size_bytes, sha256, archive_format,
       status, pinned_at, expires_at, received_at, gc_at
FROM app_source_uploads
WHERE team_id = $1::uuid AND id = $2`, teamID, id).Scan(
		&u.ID, &u.TeamID, &u.ActorUserID, &u.SizeBytes, &u.SHA256, &u.ArchiveFormat,
		&u.Status, &pinnedAt, &u.ExpiresAt, &u.ReceivedAt, &gcAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Upload{}, ErrUploadNotFound
		}
		return Upload{}, err
	}
	u.PinnedAt = pinnedAt
	u.GCAt = gcAt
	return u, nil
}

// PinUpload flips a 'received' row to 'pinned' and extends expires_at.
// Returns ErrUploadNotFound if the row is missing, scoped to a different
// team, or not in 'received' status (e.g., already pinned or gc'd).
func (r *Repository) PinUpload(ctx context.Context, teamID, id string, expiresAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `
UPDATE app_source_uploads
   SET status = 'pinned', pinned_at = NOW(), expires_at = $3
 WHERE team_id = $1::uuid AND id = $2 AND status = 'received'`,
		teamID, id, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUploadNotFound
	}
	return nil
}

// ListExpiredUploads returns uploads where expires_at < now and status is
// 'received' or 'pinned'. Used by the upload GC reconciler (T19). Returned
// Upload structs are partially populated: only ID, TeamID, Status, and
// ExpiresAt are set; other fields are zero values. Callers needing the
// full row must follow up with GetUpload.
func (r *Repository) ListExpiredUploads(ctx context.Context, limit int) ([]Upload, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id, team_id::text, status, expires_at
FROM app_source_uploads
WHERE expires_at < NOW() AND status IN ('received','pinned')
ORDER BY expires_at ASC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		var u Upload
		if err := rows.Scan(&u.ID, &u.TeamID, &u.Status, &u.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SumInertBytesByTeam returns the total size of all non-gc'd, non-expired
// uploads for the team (status IN ('received', 'pinned')). Used by T20 quota
// check.
func (r *Repository) SumInertBytesByTeam(ctx context.Context, teamID string) (int64, error) {
	var total int64
	row := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0)
		FROM app_source_uploads
		WHERE team_id = $1::uuid AND status IN ('received', 'pinned')
	`, teamID)
	if err := row.Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// CountPinnedByTeam returns the number of currently-pinned uploads for the team.
func (r *Repository) CountPinnedByTeam(ctx context.Context, teamID string) (int, error) {
	var count int
	row := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM app_source_uploads
		WHERE team_id = $1::uuid AND status = 'pinned'
	`, teamID)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// CountTeamUploadsSince returns the number of uploads created for the team
// since the given time (used for the daily rolling-window quota).
func (r *Repository) CountTeamUploadsSince(ctx context.Context, teamID string, since time.Time) (int, error) {
	var count int
	row := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM app_source_uploads
		WHERE team_id = $1::uuid AND received_at >= $2
	`, teamID, since)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// MarkUploadGCd flips the row to 'gc'd' status and stamps gc_at. Idempotent:
// a second call on an already-gc'd row is a no-op (gc_at is not re-stamped).
// No team scope parameter because GC is a privileged internal path operating
// on rows already filtered by ListExpiredUploads.
func (r *Repository) MarkUploadGCd(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `
UPDATE app_source_uploads
   SET status = 'gc''d', gc_at = NOW()
 WHERE id = $1 AND status != 'gc''d'`, id)
	return err
}
