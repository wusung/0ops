// Package cli provides the root command for the 0ops CLI.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
	"gopkg.in/yaml.v3"
)

// NewRootCommand returns the root 0ops CLI command.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "0ops",
		Short:         "0ops CLI — internal PaaS control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.Version = shared.Version
	root.SetVersionTemplate("0ops {{.Version}}\n")
	root.AddCommand(newAppsCommand())
	root.AddCommand(newMembersCommand())
	root.AddCommand(newAdminCommand())
	return root
}

func newAppsCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		pageSize  int
		cursor    string
		allPages  bool
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "apps",
		Short: "List apps",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().IntVar(&pageSize, "page-size", 50, "page size")
	cmd.PersistentFlags().StringVar(&cursor, "cursor", "", "pagination cursor")
	cmd.PersistentFlags().BoolVar(&allPages, "all", false, "fetch all pages")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List apps in the current team",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}

			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if !allPages {
				out, err := client.ListApps(ctx, ctxInfo.TeamSlug, pageSize, cursor)
				if err != nil {
					return err
				}
				return renderApps(cmd, out, outputFmt)
			}

			var merged dto.ListAppsResponse
			next := cursor
			for {
				out, err := client.ListApps(ctx, ctxInfo.TeamSlug, pageSize, next)
				if err != nil {
					return err
				}
				merged.Items = append(merged.Items, out.Items...)
				merged.PageSize = out.PageSize
				if out.NextCursor == nil {
					break
				}
				next = *out.NextCursor
			}
			return renderApps(cmd, merged, outputFmt)
		},
	})

	return cmd
}

func renderApps(cmd *cobra.Command, out dto.ListAppsResponse, outputFmt string) error {
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
		for _, item := range out.Items {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Slug, strOrDash(item.Name), strOrDash(item.RepoURL), strOrDash(item.Status))
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func strOrDash(v *string) string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return "-"
	}
	return *v
}
