package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func newSSOCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
	)

	cmd := &cobra.Command{
		Use:   "sso",
		Short: "Inspect and manage team SSO (OIDC)",
	}
	cmd.PersistentFlags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the team's SSO configuration (owner/admin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).GetSSOStatus(commandContext(cmd), ctxInfo.TeamSlug)
			if err != nil {
				return err
			}
			return renderSSOStatus(cmd, out, outputFmt)
		},
	})

	var (
		deprovUser string
		deprovYes  bool
	)
	deprovisionCmd := &cobra.Command{
		Use:   "deprovision",
		Short: "Centrally revoke a user (membership + all tokens) — owner only",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			if strings.TrimSpace(deprovUser) == "" {
				return fmt.Errorf("--user is required")
			}
			if !deprovYes {
				ok, cerr := confirmAction(cmd, fmt.Sprintf("Deprovision %q from team %q? This revokes all their tokens.", deprovUser, ctxInfo.TeamSlug))
				if cerr != nil {
					return cerr
				}
				if !ok {
					return nil
				}
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).DeprovisionSSOUser(commandContext(cmd), ctxInfo.TeamSlug, dto.SSODeprovisionRequest{User: deprovUser})
			if err != nil {
				return err
			}
			return renderSSODeprovision(cmd, out, outputFmt)
		},
	}
	deprovisionCmd.Flags().StringVar(&deprovUser, "user", "", "user email or id to deprovision")
	deprovisionCmd.Flags().BoolVar(&deprovYes, "yes", false, "skip confirmation")
	cmd.AddCommand(deprovisionCmd)

	return cmd
}

func renderSSOStatus(cmd *cobra.Command, out dto.SSOStatus, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json", "yaml":
		return renderJSONOrYAML(cmd, out, outputFmt)
	case "table":
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "PROTOCOL\tISSUER\tENFORCE\tDOMAINS(verified)\tPAT_POLICY")
		domains := make([]string, 0, len(out.Domains))
		for _, d := range out.Domains {
			mark := ""
			if d.Verified {
				mark = "(✓)"
			}
			domains = append(domains, d.Domain+mark)
		}
		domainCol := strings.Join(domains, ",")
		if domainCol == "" {
			domainCol = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\n", out.Protocol, out.Issuer, out.Enforce, domainCol, out.PATPolicy)
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderSSODeprovision(cmd *cobra.Command, out dto.SSODeprovisionResponse, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json", "yaml":
		return renderJSONOrYAML(cmd, out, outputFmt)
	case "table":
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %d tokens revoked, membership deactivated=%t\n",
			out.Message, out.TokensRevoked, out.MembershipDeactivated)
		return err
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}
