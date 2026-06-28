package server

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// auditExportHeaderIntegrity carries the base64(JSON) integrity manifest for
// CSV exports, keeping the manifest out of the CSV body (spec § 6.4).
const auditExportHeaderIntegrity = "X-0ops-Audit-Integrity"

// auditExportHeaderNextCursor carries the resume cursor for CSV exports.
const auditExportHeaderNextCursor = "X-0ops-Audit-Next-Cursor"

// auditExportGenerator identifies the producer in the integrity manifest.
const auditExportGenerator = "0ops-server"

// auditExportService is the dependency surface used by the export handler;
// production injects *audit.Service, tests inject a stub.
type auditExportService interface {
	Export(ctx context.Context, req audit.ExportRequest) (audit.ExportResult, error)
}

// exportAuditHandler streams a bounded, redacted slice of the team's audit_log
// together with an integrity manifest (audit-export-and-integrity spec § 6).
// RBAC (admin + audit:export) is enforced by the router middleware; this
// handler owns format negotiation, the mandatory `since`, and emitting the
// manifest (hard rule #7) alongside the rows.
func exportAuditHandler(svc auditExportService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		format := strings.ToLower(strings.TrimSpace(q.Get("format")))
		if format == "" {
			format = "csv"
		}
		if format != "csv" && format != "json" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "format must be csv or json", nil)
			return
		}

		// `since` is mandatory — no unbounded full-table export (hard rule #8).
		sinceRaw := strings.TrimSpace(q.Get("since"))
		if sinceRaw == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "since is required", nil)
			return
		}
		since, err := time.Parse(time.RFC3339, sinceRaw)
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid since (want RFC3339)", nil)
			return
		}
		var until time.Time
		if v := strings.TrimSpace(q.Get("until")); v != "" {
			until, err = time.Parse(time.RFC3339, v)
			if err != nil {
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid until (want RFC3339)", nil)
				return
			}
		}

		res, err := svc.Export(r.Context(), audit.ExportRequest{
			TeamID: auth.TeamID(r.Context()),
			Since:  since,
			Until:  until,
			Cursor: strings.TrimSpace(q.Get("cursor")),
		})
		if err != nil {
			switch {
			case errors.Is(err, audit.ErrExportRangeTooLarge):
				apperror.Write(w, "range_too_large", apperror.ClassUnprocessable, "export range exceeds 13 months", nil)
			case errors.Is(err, audit.ErrExportSinceRequired):
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "since is required", nil)
			default:
				apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to export audit_log", nil)
			}
			return
		}

		manifest := buildIntegritySummary(chi.URLParam(r, "team_slug"), auth.TeamID(r.Context()), res)

		if format == "json" {
			writeAuditExportJSON(w, manifest, res)
			return
		}
		writeAuditExportCSV(w, manifest, res)
	}
}

func buildIntegritySummary(teamSlug, teamID string, res audit.ExportResult) dto.IntegritySummary {
	chains := make([]dto.ChainSummary, 0, len(res.Chains))
	for _, c := range res.Chains {
		chains = append(chains, dto.ChainSummary{
			Month:       c.PartitionMonth.UTC().Format("2006-01"),
			GenesisHash: hex.EncodeToString(c.GenesisHash),
			TipHash:     hex.EncodeToString(c.TipHash),
			RowCount:    c.RowCount,
		})
	}
	return dto.IntegritySummary{
		TeamSlug:    teamSlug,
		TeamID:      teamID,
		Range:       dto.AuditExportRange{Since: res.Since.UTC(), Until: res.Until.UTC()},
		RowCount:    len(res.Rows),
		Chains:      chains,
		GeneratedAt: time.Now().UTC(),
		Generator:   auditExportGenerator,
	}
}

func newAuditExportEntry(row audit.ExportRow) dto.AuditExportEntry {
	return dto.AuditExportEntry{
		AuditLogEntry: newAuditEntry(row.Row),
		PrevHash:      hex.EncodeToString(row.PrevHash),
		RowHash:       hex.EncodeToString(row.RowHash),
	}
}

func writeAuditExportJSON(w http.ResponseWriter, manifest dto.IntegritySummary, res audit.ExportResult) {
	entries := make([]dto.AuditExportEntry, 0, len(res.Rows))
	for _, row := range res.Rows {
		entries = append(entries, newAuditExportEntry(row))
	}
	var next *string
	if res.NextCursor != "" {
		v := res.NextCursor
		next = &v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto.AuditExportEnvelope{
		Manifest:   manifest,
		Entries:    entries,
		NextCursor: next,
	})
}

func writeAuditExportCSV(w http.ResponseWriter, manifest dto.IntegritySummary, res audit.ExportResult) {
	if raw, err := json.Marshal(manifest); err == nil {
		w.Header().Set(auditExportHeaderIntegrity, base64.StdEncoding.EncodeToString(raw))
	}
	if res.NextCursor != "" {
		w.Header().Set(auditExportHeaderNextCursor, res.NextCursor)
	}
	w.Header().Set("Content-Type", "text/csv")

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{
		"id", "time", "source", "actor", "actor_user_id", "action",
		"subject_type", "subject_id", "outcome", "preview_id", "trace_id",
		"http_status", "args", "result", "prev_hash", "row_hash",
	})
	for _, row := range res.Rows {
		e := newAuditExportEntry(row)
		_ = cw.Write([]string{
			strconv.FormatInt(e.ID, 10),
			e.Time.UTC().Format(time.RFC3339Nano),
			e.Source,
			derefString(e.Actor),
			derefString(e.ActorUserID),
			e.Action,
			e.SubjectType,
			derefString(e.SubjectID),
			e.Outcome,
			derefString(e.PreviewID),
			derefString(e.TraceID),
			intPtrString(e.HTTPStatus),
			string(e.Args),
			string(e.Result),
			e.PrevHash,
			e.RowHash,
		})
	}
	cw.Flush()
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func intPtrString(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
