package reconciler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

const defaultUploadGCBatch = 100

// UploadGCStore is the DB interface the GC needs. db.Repository satisfies it.
type UploadGCStore interface {
	ListExpiredUploads(ctx context.Context, limit int) ([]db.Upload, error)
	MarkUploadGCd(ctx context.Context, id string) error
}

// UploadGCIngestStore is the filesystem interface. *ingestion.Store satisfies it.
type UploadGCIngestStore interface {
	Delete(ctx context.Context, teamID, uploadID string) error
}

// UploadGCAuditWriter optionally writes audit events. *audit.Service satisfies it.
type UploadGCAuditWriter interface {
	Log(ctx context.Context, entry audit.Entry) error
}

// UploadGCScanner deletes expired app_source_uploads rows: moves their ingest
// tree to _trash and flips status='gc'd'. Per-row failures are isolated and
// logged; the loop never aborts mid-batch.
type UploadGCScanner struct {
	Store  UploadGCStore       // T5 db.Repository
	Ingest UploadGCIngestStore // T6 ingestion.Store
	Audit  UploadGCAuditWriter // audit.Service (nil → skip audit, log Warn)
	Logger *slog.Logger        // nil → slog.Default()

	// Limit caps rows per tick. 0 → use defaultUploadGCBatch (100).
	Limit int
}

// Tick is exported for direct invocation in tests; production drives it via
// the reconciler.Runner spawn pattern.
func (s *UploadGCScanner) Tick(ctx context.Context) (processed, failed int) {
	if s == nil || s.Store == nil || s.Ingest == nil {
		return 0, 0
	}
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}
	limit := s.Limit
	if limit <= 0 {
		limit = defaultUploadGCBatch
	}
	rows, err := s.Store.ListExpiredUploads(ctx, limit)
	if err != nil {
		log.Error("upload_gc: list expired failed", "err", err)
		return 0, 0
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return processed, failed
		}
		if err := s.gcOne(ctx, log, row); err != nil {
			failed++
			log.Error("upload_gc: row failed",
				"team", row.TeamID,
				"upload", row.ID,
				"expires_at", row.ExpiresAt,
				"err", err)
			continue
		}
		processed++
	}
	return processed, failed
}

func (s *UploadGCScanner) gcOne(ctx context.Context, log *slog.Logger, row db.Upload) error {
	// Step 1: delete on disk (idempotent — T6 returns nil for missing).
	if err := s.Ingest.Delete(ctx, row.TeamID, row.ID); err != nil {
		return fmt.Errorf("ingest delete: %w", err)
	}
	// Step 2: flip DB status to gc'd (idempotent — T5 SQL uses WHERE status != 'gc''d').
	if err := s.Store.MarkUploadGCd(ctx, row.ID); err != nil {
		return fmt.Errorf("mark gc'd: %w", err)
	}
	// Step 3: audit (best-effort — failure does not roll back; the row IS gc'd).
	if s.Audit != nil {
		if err := s.Audit.Log(ctx, makeUploadGCAudit(row)); err != nil {
			log.Warn("upload_gc: audit log failed",
				"team", row.TeamID, "upload", row.ID, "err", err)
		}
	}
	return nil
}

func makeUploadGCAudit(row db.Upload) audit.Entry {
	subj := row.ID
	return audit.Entry{
		TeamID:      row.TeamID,
		ActorUserID: nil, // GC is a system event — source=system requires nil actor
		Source:      audit.SourceSystem,
		SubjectType: "upload",
		SubjectID:   &subj,
		Action:      "app_source.upload.gc_d",
		Args:        nil,
		Result: map[string]any{
			"expires_at":   row.ExpiresAt,
			"prior_status": row.Status, // "received" or "pinned" before the flip
		},
		Outcome: audit.OutcomeSuccess,
	}
}
