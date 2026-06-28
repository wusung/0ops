package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// VerifyReport is the aggregate result of verifying an export.
type VerifyReport struct {
	Chains       []audit.ChainVerdict
	PreChainRows int
	OK           bool
}

// verifyEnvelope recomputes every chain in an export envelope and folds the
// per-chain verdicts into one report (audit-export-and-integrity spec § 7).
// Rows whose months have an anchor are verified; rows with no row_hash are
// counted as pre-chain (no tamper-evidence, not a break — spec § 7.1); rows
// that carry a row_hash but whose month has no anchor are a break (an anchor
// that should exist is missing).
func verifyEnvelope(env dto.AuditExportEnvelope) VerifyReport {
	report := VerifyReport{OK: true}

	chained := map[string][]audit.VerifyRow{}
	monthsWithChainedRows := map[string]bool{}
	for _, e := range env.Entries {
		month := audit.PartitionMonth(e.Time).UTC().Format("2006-01")
		if e.RowHash == "" {
			report.PreChainRows++
			continue
		}
		chained[month] = append(chained[month], toVerifyRow(env.Manifest.TeamID, e))
		monthsWithChainedRows[month] = true
	}

	anchored := map[string]bool{}
	for _, c := range env.Manifest.Chains {
		anchored[c.Month] = true
		genesis, _ := hex.DecodeString(c.GenesisHash)
		tip, _ := hex.DecodeString(c.TipHash)
		rows := chained[c.Month]
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		verdict := audit.VerifyChain(audit.VerifyChainInput{
			TeamID:   env.Manifest.TeamID,
			Month:    monthStartFromLabel(c.Month),
			Genesis:  genesis,
			Tip:      tip,
			RowCount: c.RowCount,
			Rows:     rows,
		})
		if !verdict.OK {
			report.OK = false
		}
		report.Chains = append(report.Chains, verdict)
	}

	// Chained rows whose month has no anchor cannot be a legitimate state.
	for month := range monthsWithChainedRows {
		if !anchored[month] {
			report.OK = false
			report.Chains = append(report.Chains, audit.ChainVerdict{
				Month: month, RowCount: len(chained[month]), OK: false,
				BreakReason: "rows present with no chain anchor (manifest missing month)",
			})
		}
	}
	sort.Slice(report.Chains, func(i, j int) bool { return report.Chains[i].Month < report.Chains[j].Month })
	return report
}

func toVerifyRow(teamID string, e dto.AuditExportEntry) audit.VerifyRow {
	prev, _ := hex.DecodeString(e.PrevHash)
	rowHash, _ := hex.DecodeString(e.RowHash)
	trace := ""
	if e.TraceID != nil {
		trace = *e.TraceID
	}
	return audit.VerifyRow{
		Core: audit.Core{
			ID:          e.ID,
			TeamID:      teamID,
			ActorUserID: e.ActorUserID,
			Source:      e.Source,
			SubjectType: e.SubjectType,
			SubjectID:   e.SubjectID,
			Action:      e.Action,
			Args:        e.Args,
			Result:      e.Result,
			PreviewID:   e.PreviewID,
			TraceID:     trace,
			Outcome:     e.Outcome,
			HTTPStatus:  e.HTTPStatus,
			CreatedAt:   e.Time,
		},
		PrevHash: prev,
		RowHash:  rowHash,
	}
}

func monthStartFromLabel(label string) time.Time {
	t, err := time.Parse("2006-01", label)
	if err != nil {
		return time.Time{}
	}
	return t
}

// newAuditVerifyCommand wires `0ops audit verify` — the operator / auditor tool
// that fetches the export for a range and recomputes every chain, exiting
// non-zero on any break (spec § 7.1). Never exposed via MCP (hard rule #9).
func newAuditVerifyCommand(teamSlug, baseURL, token, _ *string) *cobra.Command {
	var since, until string

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Recompute audit_log hash chains and report any tampering",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(*teamSlug, *baseURL, *token)
			if err != nil {
				return err
			}
			ctx := commandContext(cmd)

			sinceRFC, err := normaliseAuditTime(since)
			if err != nil {
				return fmt.Errorf("invalid --since: %w", err)
			}
			if sinceRFC == "" {
				return fmt.Errorf("--since is required")
			}
			untilRFC, err := normaliseAuditTime(until)
			if err != nil {
				return fmt.Errorf("invalid --until: %w", err)
			}

			env, err := fetchChainsForVerify(ctx, ctxInfo, sinceRFC, untilRFC)
			if err != nil {
				return err
			}

			report := verifyEnvelope(env)
			out := cmd.OutOrStdout()
			broken := 0
			for _, c := range report.Chains {
				if c.OK {
					_, _ = fmt.Fprintf(out, "chain %s/%s  rows=%d  OK\n", ctxInfo.TeamSlug, c.Month, c.RowCount)
					continue
				}
				broken++
				if c.BreakID > 0 {
					_, _ = fmt.Fprintf(out, "chain %s/%s  rows=%d  BREAK at id=%d (%s)\n",
						ctxInfo.TeamSlug, c.Month, c.RowCount, c.BreakID, c.BreakReason)
				} else {
					_, _ = fmt.Fprintf(out, "chain %s/%s  rows=%d  BREAK (%s)\n",
						ctxInfo.TeamSlug, c.Month, c.RowCount, c.BreakReason)
				}
			}
			if report.PreChainRows > 0 {
				_, _ = fmt.Fprintf(out, "pre-chain rows=%d (no tamper-evidence)\n", report.PreChainRows)
			}
			if !report.OK {
				return fmt.Errorf("verify FAILED: %d chain(s) broken", broken)
			}
			_, _ = fmt.Fprintf(out, "verify OK: %d chain(s)\n", len(report.Chains))
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "lower bound (RFC3339 or duration like 24h); required")
	cmd.Flags().StringVar(&until, "until", "", "upper bound (RFC3339)")
	return cmd
}

// fetchFullExport pages through the export over an exact [since, until] window
// and returns one merged envelope (manifest from the first page, all entries).
// Used by `audit export`, which honours the caller's window verbatim.
func fetchFullExport(ctx context.Context, ctxInfo appsContext, since, until string) (dto.AuditExportEnvelope, error) {
	var merged dto.AuditExportEnvelope
	client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
	cursor := ""
	for {
		page, err := client.ExportAudit(ctx, ctxInfo.TeamSlug, backendclient.AuditExportParams{
			Since: since, Until: until, Cursor: cursor,
		})
		if err != nil {
			return dto.AuditExportEnvelope{}, err
		}
		if merged.Manifest.TeamID == "" {
			merged.Manifest = page.Manifest
		}
		merged.Entries = append(merged.Entries, page.Entries...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	merged.Manifest.RowCount = len(merged.Entries)
	return merged, nil
}

// fetchChainsForVerify fetches every (team, month) chain the requested range
// touches, in full. A chain is per-(team, month); recomputing it from genesis
// (VerifyChain) requires the WHOLE month, not just the rows inside the caller's
// window — otherwise the first in-range row's prev_hash would not chain to
// genesis and the anchor row_count would never match, yielding a false BREAK.
// So verify widens to month boundaries and fetches each month completely, while
// `audit export` keeps the exact window.
func fetchChainsForVerify(ctx context.Context, ctxInfo appsContext, sinceRFC, untilRFC string) (dto.AuditExportEnvelope, error) {
	since, err := time.Parse(time.RFC3339, sinceRFC)
	if err != nil {
		return dto.AuditExportEnvelope{}, fmt.Errorf("parse since: %w", err)
	}
	until := time.Now().UTC()
	if untilRFC != "" {
		until, err = time.Parse(time.RFC3339, untilRFC)
		if err != nil {
			return dto.AuditExportEnvelope{}, fmt.Errorf("parse until: %w", err)
		}
	}

	var merged dto.AuditExportEnvelope
	seenChain := map[string]bool{}
	for _, month := range monthsInRange(since, until) {
		monthStart := month.Format(time.RFC3339)
		monthEnd := month.AddDate(0, 1, 0).Add(-time.Microsecond).Format(time.RFC3339)
		page, err := fetchFullExport(ctx, ctxInfo, monthStart, monthEnd)
		if err != nil {
			return dto.AuditExportEnvelope{}, err
		}
		if merged.Manifest.TeamID == "" {
			merged.Manifest.TeamID = page.Manifest.TeamID
			merged.Manifest.TeamSlug = page.Manifest.TeamSlug
		}
		merged.Entries = append(merged.Entries, page.Entries...)
		for _, c := range page.Manifest.Chains {
			if !seenChain[c.Month] {
				seenChain[c.Month] = true
				merged.Manifest.Chains = append(merged.Manifest.Chains, c)
			}
		}
	}
	merged.Manifest.RowCount = len(merged.Entries)
	return merged, nil
}

// monthsInRange returns the first instant of every UTC month the [since, until]
// window touches, inclusive of both endpoints' months.
func monthsInRange(since, until time.Time) []time.Time {
	start := audit.PartitionMonth(since)
	end := audit.PartitionMonth(until)
	if end.Before(start) {
		return nil
	}
	var months []time.Time
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		months = append(months, m)
	}
	return months
}
