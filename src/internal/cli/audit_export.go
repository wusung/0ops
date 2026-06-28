package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// newAuditExportCommand wires `0ops audit export` — the forensic extraction
// surface (audit-export-and-integrity spec § 6). It pages through the export,
// merges into one envelope, and writes JSON (the offline-verifiable artifact,
// default) or CSV. `--since` is mandatory; bulk export needs admin +
// audit:export server-side (hard rules #6/#8).
func newAuditExportCommand(teamSlug, baseURL, token, _ *string) *cobra.Command {
	var since, until, format, output string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the team audit_log with an integrity manifest",
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

			switch format {
			case "", "json", "csv":
			default:
				return fmt.Errorf("unsupported --format %q (want json or csv)", format)
			}

			env, err := fetchFullExport(ctx, ctxInfo, sinceRFC, untilRFC)
			if err != nil {
				return err
			}

			w, closeFn, err := exportOutput(cmd, output)
			if err != nil {
				return err
			}
			defer closeFn()

			if format == "csv" {
				return writeExportCSV(w, env)
			}
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(env)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "lower bound (RFC3339 or duration like 24h); required")
	cmd.Flags().StringVar(&until, "until", "", "upper bound (RFC3339)")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json or csv")
	cmd.Flags().StringVar(&output, "output", "", "write to file instead of stdout")
	return cmd
}

func exportOutput(cmd *cobra.Command, path string) (writer io.Writer, closeFn func(), err error) {
	if path == "" {
		return cmd.OutOrStdout(), func() {}, nil
	}
	// path is the operator's chosen --output destination, not untrusted input.
	f, err := os.Create(path) //nolint:gosec // G304: user-specified export output file
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}

// writeExportCSV renders the merged envelope to CSV client-side from the same
// verifiable rows; the integrity manifest is emitted as a leading comment line
// so the single file is self-contained.
func writeExportCSV(w io.Writer, env dto.AuditExportEnvelope) error {
	if manifest, err := json.Marshal(env.Manifest); err == nil {
		if _, err := fmt.Fprintf(w, "# 0ops-audit-integrity %s\n", manifest); err != nil {
			return err
		}
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"id", "time", "source", "actor", "actor_user_id", "action",
		"subject_type", "subject_id", "outcome", "preview_id", "trace_id",
		"http_status", "args", "result", "prev_hash", "row_hash",
	}); err != nil {
		return err
	}
	for _, e := range env.Entries {
		if err := cw.Write([]string{
			strconv.FormatInt(e.ID, 10),
			e.Time.UTC().Format(time.RFC3339Nano),
			e.Source,
			derefStrVal(e.Actor),
			derefStrVal(e.ActorUserID),
			e.Action,
			e.SubjectType,
			derefStrVal(e.SubjectID),
			e.Outcome,
			derefStrVal(e.PreviewID),
			derefStrVal(e.TraceID),
			intPtrStr(e.HTTPStatus),
			string(e.Args),
			string(e.Result),
			e.PrevHash,
			e.RowHash,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func derefStrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func intPtrStr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
