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
	root.AddCommand(newAuthCommand())
	root.AddCommand(newAppsCommand())
	root.AddCommand(newRepoCommand())
	root.AddCommand(newDeploysCommand())
	root.AddCommand(newDomainsCommand())
	root.AddCommand(newTeamsCommand())
	root.AddCommand(newMembersCommand())
	root.AddCommand(newAuthCommand())
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
	var (
		createSlug    string
		createRepoURL string
		createRef     string
		createBuilder string
		createYes     bool
		createDryRun  bool
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
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create an app with preview/confirm flow",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			if strings.TrimSpace(createSlug) == "" {
				return fmt.Errorf("--slug is required")
			}
			if strings.TrimSpace(createRepoURL) == "" {
				return fmt.Errorf("--repo-url is required")
			}
			if strings.TrimSpace(createRef) == "" {
				return fmt.Errorf("--ref is required")
			}

			request := dto.AppCreateRequest{
				Slug:    strings.TrimSpace(createSlug),
				RepoURL: strings.TrimSpace(createRepoURL),
				Ref:     strings.TrimSpace(createRef),
			}
			if strings.TrimSpace(createBuilder) != "" {
				builder := strings.TrimSpace(createBuilder)
				request.Builder = &builder
			}

			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			ctx := commandContext(cmd)
			preview, err := client.PreviewCreateApp(ctx, ctxInfo.TeamSlug, request)
			if err != nil {
				return err
			}
			if createDryRun {
				return renderPreview(cmd, preview, outputFmt)
			}
			if !createYes {
				ok, err := confirmAction(cmd, fmt.Sprintf("Create app %q in team %q?", request.Slug, ctxInfo.TeamSlug))
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}

			out, err := client.CreateApp(ctx, ctxInfo.TeamSlug, dto.ConfirmCreateAppRequest{PreviewID: preview.PreviewID})
			if err != nil {
				return err
			}
			return renderAppCreate(cmd, out, outputFmt)
		},
	}
	createCmd.Flags().StringVar(&createSlug, "slug", "", "app slug")
	createCmd.Flags().StringVar(&createRepoURL, "repo-url", "", "source repository URL")
	createCmd.Flags().StringVar(&createRef, "ref", "main", "git ref (branch/tag)")
	createCmd.Flags().StringVar(&createBuilder, "builder", "", "optional buildpack builder")
	createCmd.Flags().BoolVar(&createYes, "yes", false, "skip confirmation")
	createCmd.Flags().BoolVar(&createDryRun, "dry-run", false, "preview only, do not confirm")
	cmd.AddCommand(createCmd)

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
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Slug, strOrDash(item.Name), strOrDash(item.RepoURL), strOrDash(item.Status))
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
		_, _ = fmt.Fprintf(w, "id\t%s\n", out.ID)
		_, _ = fmt.Fprintf(w, "team_id\t%s\n", out.TeamID)
		_, _ = fmt.Fprintf(w, "slug\t%s\n", out.Slug)
		_, _ = fmt.Fprintf(w, "name\t%s\n", strOrDash(out.Name))
		_, _ = fmt.Fprintf(w, "repo_url\t%s\n", strOrDash(out.RepoURL))
		_, _ = fmt.Fprintf(w, "repo_default_branch\t%s\n", strOrDash(out.RepoDefaultBranch))
		_, _ = fmt.Fprintf(w, "image_ref\t%s\n", strOrDash(out.ImageRef))
		_, _ = fmt.Fprintf(w, "builder\t%s\n", strOrDash(out.Builder))
		_, _ = fmt.Fprintf(w, "status\t%s\n", strOrDash(out.Status))
		_, _ = fmt.Fprintf(w, "created_at\t%s\n", out.CreatedAt.Format(time.RFC3339))
		_, _ = fmt.Fprintf(w, "updated_at\t%s\n", out.UpdatedAt.Format(time.RFC3339))
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderPreview(cmd *cobra.Command, out dto.PreviewResponse, outputFmt string) error {
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
		_, _ = fmt.Fprintf(w, "preview_id\t%s\n", out.PreviewID)
		_, _ = fmt.Fprintf(w, "action\t%s\n", out.Action)
		_, _ = fmt.Fprintf(w, "summary\t%s\n", out.Summary)
		_, _ = fmt.Fprintf(w, "expires_at\t%s\n", out.ExpiresAt.Format(time.RFC3339))
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderAppCreate(cmd *cobra.Command, out dto.AppCreateResponse, outputFmt string) error {
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
		_, _ = fmt.Fprintf(w, "app_id\t%s\n", out.AppID)
		_, _ = fmt.Fprintf(w, "app_slug\t%s\n", out.AppSlug)
		_, _ = fmt.Fprintf(w, "deploy_run_id\t%s\n", out.DeployRunID)
		_, _ = fmt.Fprintf(w, "trace_id\t%s\n", out.TraceID)
		_, _ = fmt.Fprintf(w, "subdomain_url\t%s\n", out.SubdomainURL)
		_, _ = fmt.Fprintf(w, "initial_deploy\t%t\n", out.InitialDeploy)
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func newRepoCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Inspect repo metadata",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "inspect <app-slug>",
		Short: "Inspect app repo metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).InspectRepo(commandContext(cmd), ctxInfo.TeamSlug, args[0])
			if err != nil {
				return err
			}
			return renderRepoInspect(cmd, out, outputFmt)
		},
	})

	return cmd
}

func newDeploysCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
		logLimit  int
		logFollow bool
	)

	cmd := &cobra.Command{
		Use:   "deploys",
		Short: "Inspect deploy state and logs",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "status <app-slug>",
		Short: "Get latest deploy status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).GetDeployStatus(commandContext(cmd), ctxInfo.TeamSlug, args[0])
			if err != nil {
				return err
			}
			return renderDeployStatus(cmd, out, outputFmt)
		},
	})
	logsCmd := &cobra.Command{
		Use:   "logs <app-slug>",
		Short: "Tail latest deploy logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			var out dto.TailLogsResponse
			if logFollow {
				out, err = client.TailLogsFollow(commandContext(cmd), ctxInfo.TeamSlug, args[0], logLimit, "")
			} else {
				out, err = client.TailLogs(commandContext(cmd), ctxInfo.TeamSlug, args[0], logLimit)
			}
			if err != nil {
				return err
			}
			return renderTailLogs(cmd, out, outputFmt)
		},
	}
	logsCmd.Flags().IntVar(&logLimit, "limit", 100, "max log lines")
	logsCmd.Flags().BoolVar(&logFollow, "follow", false, "stream logs via SSE")
	cmd.AddCommand(logsCmd)

	var (
		redeployRef       string
		redeployCommitSHA string
		redeployYes       bool
		redeployDryRun    bool
	)
	redeployCmd := &cobra.Command{
		Use:   "redeploy <app-slug>",
		Short: "Trigger a redeploy via preview/confirm",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			appSlug := strings.TrimSpace(args[0])
			if appSlug == "" {
				return fmt.Errorf("app-slug is required")
			}
			req := dto.RedeployRequest{}
			if strings.TrimSpace(redeployRef) != "" {
				ref := strings.TrimSpace(redeployRef)
				req.Ref = &ref
			}
			if strings.TrimSpace(redeployCommitSHA) != "" {
				sha := strings.TrimSpace(redeployCommitSHA)
				req.CommitSHA = &sha
			}
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			ctx := commandContext(cmd)
			preview, err := client.PreviewRedeploy(ctx, ctxInfo.TeamSlug, appSlug, req)
			if err != nil {
				return err
			}
			if redeployDryRun {
				return renderPreview(cmd, preview, outputFmt)
			}
			if !redeployYes {
				ok, err := confirmAction(cmd, fmt.Sprintf("Redeploy app %q in team %q?", appSlug, ctxInfo.TeamSlug))
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}
			out, err := client.Redeploy(ctx, ctxInfo.TeamSlug, dto.ConfirmRedeployRequest{PreviewID: preview.PreviewID})
			if err != nil {
				return err
			}
			return renderRedeploy(cmd, out, outputFmt)
		},
	}
	redeployCmd.Flags().StringVar(&redeployRef, "ref", "", "git ref (branch/tag); defaults to app repo_default_branch")
	redeployCmd.Flags().StringVar(&redeployCommitSHA, "commit-sha", "", "explicit commit sha; defaults to HEAD of ref")
	redeployCmd.Flags().BoolVar(&redeployYes, "yes", false, "skip confirmation")
	redeployCmd.Flags().BoolVar(&redeployDryRun, "dry-run", false, "preview only, do not confirm")
	cmd.AddCommand(redeployCmd)

	return cmd
}

func renderRedeploy(cmd *cobra.Command, out dto.RedeployResponse, outputFmt string) error {
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
		_, _ = fmt.Fprintf(w, "deploy_run_id\t%s\n", out.DeployRunID)
		_, _ = fmt.Fprintf(w, "app_id\t%s\n", out.AppID)
		_, _ = fmt.Fprintf(w, "app_slug\t%s\n", out.AppSlug)
		_, _ = fmt.Fprintf(w, "trace_id\t%s\n", out.TraceID)
		_, _ = fmt.Fprintf(w, "commit_sha\t%s\n", out.CommitSHA)
		_, _ = fmt.Fprintf(w, "ref\t%s\n", out.Ref)
		_, _ = fmt.Fprintf(w, "source\t%s\n", out.Source)
		_, _ = fmt.Fprintf(w, "subdomain_url\t%s\n", out.SubdomainURL)
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func newDomainsCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "domains",
		Short: "List app domains",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "list <app-slug>",
		Short: "List domains for app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).ListDomains(commandContext(cmd), ctxInfo.TeamSlug, args[0])
			if err != nil {
				return err
			}
			return renderDomains(cmd, out, outputFmt)
		},
	})

	return cmd
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
		RunE: func(_ *cobra.Command, args []string) error {
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

	cmd.AddCommand(newTeamsGithubCommand(&baseURL, &token))

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
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.TeamSlug, item.TeamName, item.Role, item.Plan)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderRepoInspect(cmd *cobra.Command, out dto.RepoInspectResponse, outputFmt string) error {
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
		_, _ = fmt.Fprintf(w, "app_slug\t%s\n", out.AppSlug)
		_, _ = fmt.Fprintf(w, "repo_url\t%s\n", strOrDash(out.RepoURL))
		_, _ = fmt.Fprintf(w, "repo_default_branch\t%s\n", strOrDash(out.RepoDefaultBranch))
		_, _ = fmt.Fprintf(w, "builder\t%s\n", strOrDash(out.Builder))
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderDeployStatus(cmd *cobra.Command, out dto.DeployStatusResponse, outputFmt string) error {
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
		_, _ = fmt.Fprintf(w, "deploy_id\t%s\n", out.DeployID)
		_, _ = fmt.Fprintf(w, "app_slug\t%s\n", out.AppSlug)
		_, _ = fmt.Fprintf(w, "status\t%s\n", out.Status)
		_, _ = fmt.Fprintf(w, "commit_sha\t%s\n", strOrDash(out.CommitSHA))
		_, _ = fmt.Fprintf(w, "ref\t%s\n", strOrDash(out.Ref))
		_, _ = fmt.Fprintf(w, "error_summary\t%s\n", strOrDash(out.ErrorSummary))
		if out.StartedAt != nil {
			_, _ = fmt.Fprintf(w, "started_at\t%s\n", out.StartedAt.Format(time.RFC3339))
		} else {
			_, _ = fmt.Fprintf(w, "started_at\t-\n")
		}
		if out.FinishedAt != nil {
			_, _ = fmt.Fprintf(w, "finished_at\t%s\n", out.FinishedAt.Format(time.RFC3339))
		} else {
			_, _ = fmt.Fprintf(w, "finished_at\t-\n")
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderTailLogs(cmd *cobra.Command, out dto.TailLogsResponse, outputFmt string) error {
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
			_, _ = fmt.Fprintf(w, "%s\t%s\n", item.Timestamp.Format(time.RFC3339), item.Message)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderDomains(cmd *cobra.Command, out dto.ListDomainsResponse, outputFmt string) error {
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
			kind := "-"
			if item.Kind != nil {
				kind = *item.Kind
			}
			verifiedAt := "-"
			if item.VerifiedAt != nil {
				verifiedAt = item.VerifiedAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", item.Hostname, kind, item.Verified, verifiedAt)
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
