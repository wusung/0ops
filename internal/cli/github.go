package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/winshare/zeroops/internal/shared/backendclient"
)

const (
	githubInstallPollInterval = 3 * time.Second
	githubInstallPollTimeout  = 10 * time.Minute
)

func newTeamsGithubCommand(parentBaseURL, parentToken *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Manage GitHub App integration for the team",
	}
	cmd.AddCommand(newGithubInstallCommand(parentBaseURL, parentToken))
	cmd.AddCommand(newGithubUninstallCommand(parentBaseURL, parentToken))
	cmd.AddCommand(newGithubStatusCommand(parentBaseURL, parentToken))
	return cmd
}

func newGithubInstallCommand(parentBaseURL, parentToken *string) *cobra.Command {
	var (
		teamSlug     string
		yesFlag      bool
		statusOnly   bool
		pollOverride time.Duration
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install GitHub App for the team (preview -> confirm -> open browser -> poll)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, *parentBaseURL, *parentToken)
			if err != nil {
				return err
			}
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			ctx := commandContext(cmd)

			if statusOnly {
				return printInstallStatus(cmd, client, ctx, ctxInfo.TeamSlug)
			}

			preview, err := client.PreviewGitHubInstall(ctx, ctxInfo.TeamSlug)
			if err != nil {
				return fmt.Errorf("preview failed: %w", err)
			}
			renderPreviewSummary(cmd, preview.Summary, []string{
				"Generate install URL and redirect to GitHub for approval",
			})

			if !yesFlag {
				ok, err := confirmAction(cmd, fmt.Sprintf("Install GitHub App for team %q?", ctxInfo.TeamSlug))
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("installation cancelled")
				}
			}

			confirm, err := client.ConfirmGitHubInstall(ctx, ctxInfo.TeamSlug, preview.PreviewID)
			if err != nil {
				return fmt.Errorf("confirm failed: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nOpen this URL in a browser to finish the install:\n  %s\n\n", confirm.InstallURL)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The link expires at %s.\n", confirm.ExpiresAt.UTC().Format(time.RFC3339))

			interval := githubInstallPollInterval
			if pollOverride > 0 {
				interval = pollOverride
			}
			return pollInstallStatus(cmd, client, ctx, ctxInfo.TeamSlug, interval, githubInstallPollTimeout)
		},
	}
	cmd.Flags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation")
	cmd.Flags().BoolVar(&statusOnly, "status", false, "only check whether install is complete")
	cmd.Flags().DurationVar(&pollOverride, "poll-interval", 0, "override polling interval")
	return cmd
}

func newGithubUninstallCommand(parentBaseURL, parentToken *string) *cobra.Command {
	var (
		teamSlug string
		yesFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall GitHub App from the team (preview -> confirm)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, *parentBaseURL, *parentToken)
			if err != nil {
				return err
			}
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			ctx := commandContext(cmd)

			preview, err := client.PreviewGitHubUninstall(ctx, ctxInfo.TeamSlug)
			if err != nil {
				return fmt.Errorf("preview failed: %w", err)
			}
			renderPreviewSummary(cmd, preview.Summary, []string{
				"DELETE GitHub App installation (irreversible)",
				"Pause every app in the team",
				"Drop cached installation tokens",
			})
			if !yesFlag {
				ok, err := confirmAction(cmd, fmt.Sprintf("Uninstall GitHub App from team %q?", ctxInfo.TeamSlug))
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("uninstall cancelled")
				}
			}
			result, err := client.ConfirmGitHubUninstall(ctx, ctxInfo.TeamSlug, preview.PreviewID)
			if err != nil {
				return fmt.Errorf("confirm failed: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Uninstall result: status=%s paused_app_count=%d\n", result.Status, result.PausedAppCount)
			return err
		},
	}
	cmd.Flags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation")
	return cmd
}

func newGithubStatusCommand(parentBaseURL, parentToken *string) *cobra.Command {
	var teamSlug string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the GitHub App is installed for the team",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, *parentBaseURL, *parentToken)
			if err != nil {
				return err
			}
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			return printInstallStatus(cmd, client, commandContext(cmd), ctxInfo.TeamSlug)
		},
	}
	cmd.Flags().StringVar(&teamSlug, "team", "", "team slug")
	return cmd
}

func printInstallStatus(cmd *cobra.Command, client *backendclient.Client, ctx context.Context, teamSlug string) error {
	out, err := client.GetGitHubInstallStatus(ctx, teamSlug)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "team\t%s\n", teamSlug)
	_, _ = fmt.Fprintf(w, "installed\t%t\n", out.Installed)
	if out.GithubInstallID != nil {
		_, _ = fmt.Fprintf(w, "github_install_id\t%d\n", *out.GithubInstallID)
	} else {
		_, _ = fmt.Fprintf(w, "github_install_id\t-\n")
	}
	return w.Flush()
}

func pollInstallStatus(cmd *cobra.Command, client *backendclient.Client, ctx context.Context, teamSlug string, interval, timeout time.Duration) error {
	if interval <= 0 {
		interval = githubInstallPollInterval
	}
	if timeout <= 0 {
		timeout = githubInstallPollTimeout
	}
	deadline := time.Now().Add(timeout)
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Waiting for install completion (Ctrl+C to abort)...")
	for {
		status, err := client.GetGitHubInstallStatus(ctx, teamSlug)
		if err != nil {
			return err
		}
		if status.Installed {
			id := int64(0)
			if status.GithubInstallID != nil {
				id = *status.GithubInstallID
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Install succeeded (installation_id=%d).\n", id)
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("install polling timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func renderPreviewSummary(cmd *cobra.Command, summary string, sideEffects []string) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nAction: %s\nSide effects:\n", strings.TrimSpace(summary))
	for i, eff := range sideEffects {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s\n", i+1, eff)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout())
}
