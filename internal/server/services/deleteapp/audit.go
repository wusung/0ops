package deleteapp

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/db"
)

// isNotFound returns true for the pgx.ErrNoRows marker that the
// Repository.GetTeamAppBySlug helper returns when no app row exists.
func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// auditResidueSuccess composes the audit_log row written on cleanup_residue
// success. The naming keeps it close to the cleanup.go call site without
// polluting the package API surface.
func auditResidueSuccess(job ResidueJob, appID string, previewID, traceID *string) db.AuditLogInsert {
	return db.AuditLogInsert{
		TeamID:      job.TeamID,
		SubjectType: SubjectType,
		SubjectID:   &appID,
		Action:      "delete_app_cleanup_residue",
		Args:        job.Payload,
		Result:      map[string]any{"status": "deleted"},
		PreviewID:   previewID,
		TraceID:     traceID,
	}
}

// auditResidueFailed records the cleanup_residue terminal failure
// audit row (spec § 6.2 `failed_permanently`).
func auditResidueFailed(job ResidueJob, appID, msg string) db.AuditLogInsert {
	return db.AuditLogInsert{
		TeamID:      job.TeamID,
		SubjectType: SubjectType,
		SubjectID:   &appID,
		Action:      "delete_app_cleanup_residue_failed",
		Args:        job.Payload,
		Result:      map[string]any{"status": "failed_permanently", "error": msg},
	}
}
