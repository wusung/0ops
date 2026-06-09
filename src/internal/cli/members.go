package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
	"gopkg.in/yaml.v3"
)

func newMembersCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "members",
		Short: "Manage team members",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List team members",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).ListMembers(commandContext(cmd), ctxInfo.TeamSlug)
			if err != nil {
				return err
			}
			return renderMembers(cmd, out, outputFmt)
		},
	})

	var (
		inviteRole        string
		inviteGitHubLogin string
		inviteEmail       string
	)
	previewInviteCmd := &cobra.Command{
		Use:   "preview-invite",
		Short: "Create invite preview",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			req := dto.InviteMemberRequest{
				Role:        inviteRole,
				GithubLogin: stringPtrIfNotEmpty(inviteGitHubLogin),
				Email:       stringPtrIfNotEmpty(inviteEmail),
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).PreviewInviteMember(commandContext(cmd), ctxInfo.TeamSlug, req)
			if err != nil {
				return err
			}
			return renderJSONOrYAML(cmd, out, outputFmt)
		},
	}
	previewInviteCmd.Flags().StringVar(&inviteRole, "role", "member", "member role")
	previewInviteCmd.Flags().StringVar(&inviteGitHubLogin, "github-login", "", "github login")
	previewInviteCmd.Flags().StringVar(&inviteEmail, "email", "", "email")
	cmd.AddCommand(previewInviteCmd)

	var invitePreviewID string
	inviteCmd := &cobra.Command{
		Use:   "invite",
		Short: "Confirm invite with preview_id",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).InviteMember(commandContext(cmd), ctxInfo.TeamSlug, dto.ConfirmInviteMemberRequest{
				PreviewID: invitePreviewID,
			})
			if err != nil {
				return err
			}
			return renderJSONOrYAML(cmd, out, outputFmt)
		},
	}
	inviteCmd.Flags().StringVar(&invitePreviewID, "preview-id", "", "preview id")
	_ = inviteCmd.MarkFlagRequired("preview-id")
	cmd.AddCommand(inviteCmd)

	var removeUserID string
	previewRemoveCmd := &cobra.Command{
		Use:   "preview-remove",
		Short: "Create remove preview",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).PreviewRemoveMember(commandContext(cmd), ctxInfo.TeamSlug, dto.RemoveMemberRequest{
				UserID: removeUserID,
			})
			if err != nil {
				return err
			}
			return renderJSONOrYAML(cmd, out, outputFmt)
		},
	}
	previewRemoveCmd.Flags().StringVar(&removeUserID, "user-id", "", "target user id")
	_ = previewRemoveCmd.MarkFlagRequired("user-id")
	cmd.AddCommand(previewRemoveCmd)

	var removePreviewID string
	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Confirm remove with preview_id",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			if err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).RemoveMember(commandContext(cmd), ctxInfo.TeamSlug, dto.ConfirmRemoveMemberRequest{
				PreviewID: removePreviewID,
			}); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "removed")
			return err
		},
	}
	removeCmd.Flags().StringVar(&removePreviewID, "preview-id", "", "preview id")
	_ = removeCmd.MarkFlagRequired("preview-id")
	cmd.AddCommand(removeCmd)

	return cmd
}

func newAdminCommand() *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin utilities",
	}
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")

	var (
		teamSlug    string
		teamName    string
		githubLogin string
		email       string
		outputFmt   string
	)
	bootstrapCmd := &cobra.Command{
		Use:   "bootstrap-owner",
		Short: "Initialize first owner and team",
		RunE: func(cmd *cobra.Command, _ []string) error {
			host := firstNonEmpty(baseURL, envOr("OPS_HOST", "http://127.0.0.1:8080"))
			out, err := backendclient.New(host, "").BootstrapOwner(commandContext(cmd), dto.BootstrapOwnerRequest{
				TeamSlug:    teamSlug,
				TeamName:    teamName,
				GithubLogin: githubLogin,
				Email:       stringPtrIfNotEmpty(email),
			})
			if err != nil {
				return err
			}
			return renderJSONOrYAML(cmd, out, outputFmt)
		},
	}
	bootstrapCmd.Flags().StringVar(&teamSlug, "team-slug", "", "team slug")
	bootstrapCmd.Flags().StringVar(&teamName, "team-name", "", "team name")
	bootstrapCmd.Flags().StringVar(&githubLogin, "github-login", "", "owner github login")
	bootstrapCmd.Flags().StringVar(&email, "email", "", "owner email")
	bootstrapCmd.Flags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "json"), "output format")
	_ = bootstrapCmd.MarkFlagRequired("team-slug")
	_ = bootstrapCmd.MarkFlagRequired("team-name")
	_ = bootstrapCmd.MarkFlagRequired("github-login")
	cmd.AddCommand(bootstrapCmd)

	var (
		retryTeamSlug string
		retryAppSlug  string
		retryOutput   string
	)
	retryDeleteCmd := &cobra.Command{
		Use:   "retry-delete",
		Short: "Re-drive an app delete stuck in 'deleting' (recovery)",
		Long: `Re-enqueue a cleanup_residue job for an app stuck in 'deleting' whose
original job went failed_permanently. See docs/runbooks/delete-app-residue.md.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host := firstNonEmpty(baseURL, envOr("OPS_HOST", "http://127.0.0.1:8080"))
			out, err := backendclient.New(host, "").RetryStuckDelete(commandContext(cmd), dto.AdminRetryDeleteRequest{
				TeamSlug: retryTeamSlug,
				AppSlug:  retryAppSlug,
			})
			if err != nil {
				return err
			}
			return renderJSONOrYAML(cmd, out, retryOutput)
		},
	}
	retryDeleteCmd.Flags().StringVar(&retryTeamSlug, "team-slug", "", "team slug")
	retryDeleteCmd.Flags().StringVar(&retryAppSlug, "app-slug", "", "app slug")
	retryDeleteCmd.Flags().StringVar(&retryOutput, "output", envOr("OPS_OUTPUT", "json"), "output format")
	_ = retryDeleteCmd.MarkFlagRequired("team-slug")
	_ = retryDeleteCmd.MarkFlagRequired("app-slug")
	cmd.AddCommand(retryDeleteCmd)

	return cmd
}

func renderMembers(cmd *cobra.Command, out dto.ListMembersResponse, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json", "yaml":
		return renderJSONOrYAML(cmd, out, outputFmt)
	case "table":
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		for _, item := range out.Items {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.UserID, strOrDash(item.GithubLogin), strOrDash(item.Email), item.Role)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderJSONOrYAML(cmd *cobra.Command, out any, outputFmt string) error {
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
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func commandContext(cmd *cobra.Command) context.Context {
	ctx := cmd.Context()
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func stringPtrIfNotEmpty(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}
