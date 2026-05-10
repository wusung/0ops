// Package cli provides the root command for the 0ops CLI.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
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
	cmd.PersistentFlags().StringVar(&teamSlug, "team", os.Getenv("OPS_TEAM"), "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", envOr("OPS_HOST", "http://127.0.0.1:8080"), "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", os.Getenv("OPS_BEARER_TOKEN"), "bearer token")
	cmd.PersistentFlags().IntVar(&pageSize, "page-size", 50, "page size")
	cmd.PersistentFlags().StringVar(&cursor, "cursor", "", "pagination cursor")
	cmd.PersistentFlags().BoolVar(&allPages, "all", false, "fetch all pages")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List apps in the current team",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(teamSlug) == "" {
				return fmt.Errorf("no team in context. run 0ops teams use <slug> or pass --team")
			}

			client := backendclient.New(baseURL, token)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if !allPages {
				out, err := client.ListApps(ctx, teamSlug, pageSize, cursor)
				if err != nil {
					return err
				}
				return renderApps(cmd, out, outputFmt)
			}

			var merged dto.ListAppsResponse
			next := cursor
			for {
				out, err := client.ListApps(ctx, teamSlug, pageSize, next)
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
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
