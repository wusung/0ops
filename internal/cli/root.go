// Package cli provides the root command for the 0ops CLI.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared"
	"github.com/winshare/zeroops/internal/shared/authconfig"
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
	root.AddCommand(newTeamsCommand())
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
	cmd.AddCommand(&cobra.Command{
		Use:   "get <slug>",
		Short: "Get an app in the current team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}

			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			out, err := client.GetApp(ctx, ctxInfo.TeamSlug, args[0])
			if err != nil {
				return err
			}
			return renderApp(cmd, out, outputFmt)
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

func renderApp(cmd *cobra.Command, out dto.AppRef, outputFmt string) error {
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
		fmt.Fprintf(w, "id\t%s\n", out.ID)
		fmt.Fprintf(w, "team_id\t%s\n", out.TeamID)
		fmt.Fprintf(w, "slug\t%s\n", out.Slug)
		fmt.Fprintf(w, "name\t%s\n", strOrDash(out.Name))
		fmt.Fprintf(w, "repo_url\t%s\n", strOrDash(out.RepoURL))
		fmt.Fprintf(w, "repo_default_branch\t%s\n", strOrDash(out.RepoDefaultBranch))
		fmt.Fprintf(w, "image_ref\t%s\n", strOrDash(out.ImageRef))
		fmt.Fprintf(w, "builder\t%s\n", strOrDash(out.Builder))
		fmt.Fprintf(w, "status\t%s\n", strOrDash(out.Status))
		fmt.Fprintf(w, "created_at\t%s\n", out.CreatedAt.Format(time.RFC3339))
		fmt.Fprintf(w, "updated_at\t%s\n", out.UpdatedAt.Format(time.RFC3339))
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func newTeamsCommand() *cobra.Command {
	var (
		baseURL   string
		token     string
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "teams",
		Short: "List or select teams",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List teams for the current actor",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveBackendContext(baseURL, token)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).ListTeams(ctx)
			if err != nil {
				return err
			}
			return renderTeams(cmd, out, outputFmt)
		},
	})

	useCmd := &cobra.Command{
		Use:   "use <slug>",
		Short: "Set the default team for the current host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := authconfig.Load()
			if err != nil {
				return err
			}

			host := resolveHost(baseURL, cfg)
			if ok := cfg.SetDefaultTeamForHost(host, args[0]); !ok {
				return fmt.Errorf("no auth config entry found for host %q", host)
			}
			return authconfig.Save(cfg)
		},
	}

	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")
	cmd.AddCommand(useCmd)

	return cmd
}

func renderTeams(cmd *cobra.Command, out dto.ListTeamsResponse, outputFmt string) error {
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
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.TeamSlug, item.TeamName, item.Role, item.Plan)
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
