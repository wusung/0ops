package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// newAuditCommand wires the `0ops audit` subtree (list / get).
// audit-log spec § 7.1 defines the surface; the CLI is read-only.
func newAuditCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query the team audit_log",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(newAuditListCommand(&teamSlug, &baseURL, &token, &outputFmt))
	cmd.AddCommand(newAuditGetCommand(&teamSlug, &baseURL, &token, &outputFmt))
	cmd.AddCommand(newAuditExportCommand(&teamSlug, &baseURL, &token, &outputFmt))
	cmd.AddCommand(newAuditVerifyCommand(&teamSlug, &baseURL, &token, &outputFmt))
	return cmd
}

func newAuditListCommand(teamSlug, baseURL, token, outputFmt *string) *cobra.Command {
	var (
		since    string
		until    string
		action   string
		actor    string
		trace    string
		pageSize int
		cursor   string
		allPages bool
	)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List audit_log entries for the current team",
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
			untilRFC, err := normaliseAuditTime(until)
			if err != nil {
				return fmt.Errorf("invalid --until: %w", err)
			}

			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			next := cursor
			var merged dto.ListAuditResponse
			for {
				params := backendclient.AuditListParams{
					Since:    sinceRFC,
					Until:    untilRFC,
					Action:   action,
					Actor:    actor,
					TraceID:  trace,
					PageSize: pageSize,
					Cursor:   next,
				}
				out, err := client.ListAudit(ctx, ctxInfo.TeamSlug, params)
				if err != nil {
					return err
				}
				merged.Items = append(merged.Items, out.Items...)
				merged.PageSize = out.PageSize
				merged.NextCursor = out.NextCursor
				if !allPages || out.NextCursor == nil {
					break
				}
				next = *out.NextCursor
			}
			return renderAuditList(cmd, merged, *outputFmt)
		},
	}
	listCmd.Flags().StringVar(&since, "since", "", "lower bound (RFC3339 or duration like 24h)")
	listCmd.Flags().StringVar(&until, "until", "", "upper bound (RFC3339)")
	listCmd.Flags().StringVar(&action, "action", "", "action prefix (e.g. create_app)")
	listCmd.Flags().StringVar(&actor, "actor", "", "actor github_login, user_id, or `me`")
	listCmd.Flags().StringVar(&trace, "trace", "", "trace id filter")
	listCmd.Flags().IntVar(&pageSize, "page-size", 50, "page size (max 200)")
	listCmd.Flags().StringVar(&cursor, "cursor", "", "pagination cursor")
	listCmd.Flags().BoolVar(&allPages, "all", false, "fetch every page")
	return listCmd
}

func newAuditGetCommand(teamSlug, baseURL, token, outputFmt *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single audit_log entry by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(*teamSlug, *baseURL, *token)
			if err != nil {
				return err
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid id %q", args[0])
			}
			ctx := commandContext(cmd)
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).GetAudit(ctx, ctxInfo.TeamSlug, id)
			if err != nil {
				return err
			}
			return renderAuditEntry(cmd, out, *outputFmt)
		},
	}
}

func renderAuditList(cmd *cobra.Command, out dto.ListAuditResponse, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "yaml":
		data, err := yaml.Marshal(out)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	case "table":
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tTIME\tACTOR\tACTION\tOUTCOME\tSUBJECT")
		for _, item := range out.Items {
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
				item.ID,
				item.Time.Format(time.RFC3339),
				strOrDashNonPtr(actorLabel(item)),
				item.Action,
				item.Outcome,
				subjectLabel(item),
			)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderAuditEntry(cmd *cobra.Command, out dto.AuditLogEntry, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json", "table":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "yaml":
		data, err := yaml.Marshal(out)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func actorLabel(item dto.AuditLogEntry) string {
	if item.Actor != nil && *item.Actor != "" {
		return *item.Actor
	}
	if item.ActorUserID != nil && *item.ActorUserID != "" {
		return *item.ActorUserID
	}
	return "system:" + item.Source
}

func subjectLabel(item dto.AuditLogEntry) string {
	if item.SubjectType == "" {
		return "-"
	}
	if item.SubjectID != nil && *item.SubjectID != "" {
		return item.SubjectType + "/" + *item.SubjectID
	}
	return item.SubjectType
}

func strOrDashNonPtr(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// normaliseAuditTime accepts RFC3339 or a Go duration (e.g. 24h, 7d-ish).
// Empty stays empty (server applies the spec § 6.1 default window).
func normaliseAuditTime(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	dur, err := parseAuditDuration(raw)
	if err != nil {
		return "", err
	}
	return time.Now().UTC().Add(-dur).Format(time.RFC3339), nil
}

// parseAuditDuration accepts the standard Go formats plus a trailing
// `d` for days (e.g. `7d`).
func parseAuditDuration(raw string) (time.Duration, error) {
	if strings.HasSuffix(raw, "d") {
		head := strings.TrimSuffix(raw, "d")
		n, err := strconv.Atoi(head)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}
