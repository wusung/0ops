package audit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Export (audit-export-and-integrity spec § 6 / ADR-0015 § 3.4) is the forensic
// extraction surface: it streams the redacted, as-stored rows for a bounded
// time range together with an integrity manifest (the per-(team, month) chain
// genesis / tip / row_count). The exported rows carry their prev_hash / row_hash
// so an auditor can recompute the chain offline and match it against the
// manifest — the "export ↔ chain ↔ verify" closed loop (spec § 6.4, DD2).

// exportMaxRows is the soft per-response row cap (spec § 6.3). A response that
// reaches it returns a NextCursor so the client resumes. It is a var so tests
// can exercise the cursor branch without materialising 100k rows.
var exportMaxRows = 100000

// exportMaxRange bounds since→until at the 13-month retention window so export
// can never request an unbounded full-table scan (spec § 6.1, hard rule #8).
const exportMaxRangeMonths = 13

// ExportRequest is the wire-level export request after RBAC has passed.
type ExportRequest struct {
	TeamID string
	Since  time.Time
	Until  time.Time
	Cursor string
}

// ExportRow is a row projected for export / verify: every hash-covered Core
// field (via the embedded Row) plus the stored prev_hash / row_hash, so an
// auditor can recompute row_hash = SHA-256(prev || canonical(core)) and check
// linkage without database access.
type ExportRow struct {
	Row
	PrevHash []byte
	RowHash  []byte
}

// ChainHead mirrors one audit_chain_head anchor row for the integrity manifest.
type ChainHead struct {
	PartitionMonth time.Time
	GenesisHash    []byte
	TipHash        []byte
	RowCount       int64
}

// ExportResult is the assembled page: capped rows, the chain anchors the range
// touches, and a resume cursor when more rows remain.
type ExportResult struct {
	Rows       []ExportRow
	Chains     []ChainHead
	Since      time.Time
	Until      time.Time
	NextCursor string
}

// ExportFilter is the persistence-layer request for a page of exportable rows,
// ascending by (created_at, id) after Cursor, up to Limit rows.
type ExportFilter struct {
	TeamID string
	Since  time.Time
	Until  time.Time
	Cursor string
	Limit  int
}

// ExportReader is the persistence boundary for export. The production
// *db.Repository implements it; the query Reader is type-asserted to it.
type ExportReader interface {
	ExportAuditLog(ctx context.Context, filter ExportFilter) ([]ExportRow, error)
	ListChainHeads(ctx context.Context, teamID string, sinceMonth, untilMonth time.Time) ([]ChainHead, error)
}

// ErrExportSinceRequired is returned when no `since` was supplied; an unbounded
// export is forbidden (spec § 6.1, hard rule #8).
var ErrExportSinceRequired = errors.New("audit: export requires since")

// ErrExportRangeTooLarge is returned when until-since exceeds the 13-month
// retention window (spec § 6.1, hard rule #8).
var ErrExportRangeTooLarge = errors.New("audit: export range exceeds 13 months")

// ErrExportUnsupported is returned when the configured reader cannot export.
var ErrExportUnsupported = errors.New("audit: export not supported by reader")

// Export validates the request, fetches one capped page of rows, and assembles
// the integrity manifest from the chain heads the range touches.
func (s *Service) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
	if s == nil {
		return ExportResult{}, errors.New("audit: nil service")
	}
	exporter, ok := s.reader.(ExportReader)
	if !ok {
		return ExportResult{}, ErrExportUnsupported
	}

	req, err := normaliseExportRequest(req)
	if err != nil {
		s.observer.ObserveQuery("export", "validation_error")
		return ExportResult{}, err
	}

	rows, err := exporter.ExportAuditLog(ctx, ExportFilter{
		TeamID: req.TeamID,
		Since:  req.Since,
		Until:  req.Until,
		Cursor: req.Cursor,
		Limit:  exportMaxRows + 1, // one beyond the cap detects a further page
	})
	if err != nil {
		s.observer.ObserveQuery("export", "read_error")
		return ExportResult{}, fmt.Errorf("audit: export rows: %w", err)
	}

	var nextCursor string
	if len(rows) > exportMaxRows {
		rows = rows[:exportMaxRows]
		last := rows[len(rows)-1]
		nextCursor = EncodeCursor(last.CreatedAt, last.ID)
	}

	chains, err := exporter.ListChainHeads(ctx, req.TeamID,
		PartitionMonth(req.Since), PartitionMonth(req.Until))
	if err != nil {
		s.observer.ObserveQuery("export", "read_error")
		return ExportResult{}, fmt.Errorf("audit: export chains: %w", err)
	}

	s.observer.ObserveQuery("export", "success")
	return ExportResult{
		Rows:       rows,
		Chains:     chains,
		Since:      req.Since,
		Until:      req.Until,
		NextCursor: nextCursor,
	}, nil
}

func normaliseExportRequest(in ExportRequest) (ExportRequest, error) {
	out := in
	if out.TeamID == "" {
		return ExportRequest{}, errors.New("audit: export requires team_id")
	}
	if out.Since.IsZero() {
		return ExportRequest{}, ErrExportSinceRequired
	}
	out.Since = out.Since.UTC()
	if out.Until.IsZero() {
		out.Until = time.Now().UTC()
	} else {
		out.Until = out.Until.UTC()
	}
	if out.Since.After(out.Until) {
		return ExportRequest{}, errors.New("audit: export since must be <= until")
	}
	if out.Until.After(out.Since.AddDate(0, exportMaxRangeMonths, 0)) {
		return ExportRequest{}, ErrExportRangeTooLarge
	}
	return out, nil
}
