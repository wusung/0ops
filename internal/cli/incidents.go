package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// newIncidentsCommand wires `0ops incidents` subtree (list / get / close).
// The CLI is the spec § 16 #8 mandated entry point for close — direct
// SQL is prohibited so this command path captures the audit_log entry.
func newIncidentsCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
	)
	cmd := &cobra.Command{
		Use:   "incidents",
		Short: "Inspect and close incidents",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(newIncidentsListCommand(&teamSlug, &baseURL, &token, &outputFmt))
	cmd.AddCommand(newIncidentsGetCommand(&teamSlug, &baseURL, &token, &outputFmt))
	cmd.AddCommand(newIncidentsCloseCommand(&teamSlug, &baseURL, &token, &outputFmt))
	return cmd
}

func newIncidentsListCommand(teamSlug, baseURL, token, outputFmt *string) *cobra.Command {
	var (
		status   string
		kind     string
		severity string
		pageSize int
		cursor   string
		allPages bool
	)
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List incidents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(*teamSlug, *baseURL, *token)
			if err != nil {
				return err
			}
			ctx := commandContext(cmd)
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			next := cursor
			var merged dto.ListIncidentsResponse
			for {
				params := backendclient.IncidentListParams{
					Status:   status,
					Kind:     kind,
					Severity: severity,
					PageSize: pageSize,
					Cursor:   next,
				}
				out, err := client.ListIncidents(ctx, ctxInfo.TeamSlug, params)
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
			return renderIncidentList(cmd, merged, *outputFmt)
		},
	}
	listCmd.Flags().StringVar(&status, "status", "open", "filter: open|closed|all")
	listCmd.Flags().StringVar(&kind, "kind", "", "filter by kind")
	listCmd.Flags().StringVar(&severity, "severity", "", "filter by severity")
	listCmd.Flags().IntVar(&pageSize, "page-size", 50, "page size (max 200)")
	listCmd.Flags().StringVar(&cursor, "cursor", "", "pagination cursor")
	listCmd.Flags().BoolVar(&allPages, "all", false, "fetch every page")
	return listCmd
}

func newIncidentsGetCommand(teamSlug, baseURL, token, outputFmt *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single incident by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(*teamSlug, *baseURL, *token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).GetIncident(commandContext(cmd), ctxInfo.TeamSlug, strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			return renderIncident(cmd, out, *outputFmt)
		},
	}
}

func newIncidentsCloseCommand(teamSlug, baseURL, token, outputFmt *string) *cobra.Command {
	var note string
	closeCmd := &cobra.Command{
		Use:   "close <id>",
		Short: "Close an open incident (writes audit_log)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(*teamSlug, *baseURL, *token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).
				CloseIncident(commandContext(cmd), ctxInfo.TeamSlug, strings.TrimSpace(args[0]), dto.CloseIncidentRequest{Note: strings.TrimSpace(note)})
			if err != nil {
				return err
			}
			return renderIncident(cmd, out, *outputFmt)
		},
	}
	closeCmd.Flags().StringVar(&note, "note", "", "free-form close note (e.g. root cause)")
	return closeCmd
}

func renderIncidentList(cmd *cobra.Command, out dto.ListIncidentsResponse, outputFmt string) error {
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
		_, _ = fmt.Fprintln(w, "ID\tOPENED\tSUBJECT\tKIND\tSEVERITY\tSTATUS")
		for _, item := range out.Items {
			status := "open"
			if item.ClosedAt != nil {
				status = "closed"
			}
			subject := item.SubjectType + "/" + item.SubjectID
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				item.ID,
				item.OpenedAt.Format(time.RFC3339),
				subject,
				item.Kind,
				item.Severity,
				status,
			)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderIncident(cmd *cobra.Command, out dto.Incident, outputFmt string) error {
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
