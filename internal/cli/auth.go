package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	osuser "os/user"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/winshare/zeroops/internal/shared/authconfig"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func newAuthCommand() *cobra.Command {
	var (
		hostFlag    string
		tokenFlag   string
		githubLogin string
		email       string
		outputFmt   string
	)

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate CLI with backend",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthLogin(cmd, hostFlag, githubLogin, email)
		},
	}
	cmd.PersistentFlags().StringVar(&hostFlag, "host", "", "backend host (default: --host > OPS_HOST > auth.json host > http://127.0.0.1:8080)")
	cmd.PersistentFlags().StringVar(&tokenFlag, "token", "", "bearer token (default: --token > OPS_BEARER_TOKEN > auth.json)")
	cmd.PersistentFlags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Login via GitHub identity and persist bearer token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthLogin(cmd, hostFlag, githubLogin, email)
		},
	}
	loginCmd.Flags().StringVar(&githubLogin, "github-login", "", "github login (default: --github-login > OPS_GITHUB_LOGIN > GITHUB_LOGIN > auth.json same-host > localhost:owner > shell user)")
	loginCmd.Flags().StringVar(&email, "email", "", "email")
	cmd.AddCommand(loginCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Revoke current token and remove local credential",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveBackendContext(hostFlag, tokenFlag)
			if err != nil {
				return err
			}
			if err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).Logout(commandContext(cmd)); err != nil {
				return err
			}
			cfg, err := authconfig.Load()
			if err == nil {
				cfg.RemoveHost(ctxInfo.Host)
				if err := authconfig.Save(cfg); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "logged out from %s\n", ctxInfo.Host)
			return err
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show stored auth status for host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := authconfig.Load()
			if err != nil {
				return err
			}
			host := resolveHost(hostFlag, cfg)
			token, ok := cfg.TokenForHost(host)
			if !ok {
				return fmt.Errorf("no auth config entry for host %q", host)
			}
			switch strings.ToLower(outputFmt) {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(token)
			case "table":
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintf(w, "host\t%s\n", token.Host)
				fmt.Fprintf(w, "github_login\t%s\n", token.GitHubLogin)
				fmt.Fprintf(w, "default_team_slug\t%s\n", token.DefaultTeamSlug)
				return w.Flush()
			default:
				return fmt.Errorf("unsupported output format %q", outputFmt)
			}
		},
	})

	return cmd
}

func resolveGitHubLogin(githubLoginFlag, host string, cfg authconfig.File) string {
	explicit := firstNonEmpty(githubLoginFlag, os.Getenv("OPS_GITHUB_LOGIN"), os.Getenv("GITHUB_LOGIN"))
	if explicit != "" {
		return explicit
	}
	if fromFile, ok := cfg.TokenForHost(host); ok {
		if strings.TrimSpace(fromFile.GitHubLogin) != "" {
			return fromFile.GitHubLogin
		}
	}
	if first, ok := cfg.First(); ok && strings.TrimSpace(first.GitHubLogin) != "" {
		return first.GitHubLogin
	}
	if isLocalHost(host) {
		return "owner"
	}
	if shellUser := firstNonEmpty(os.Getenv("USER"), os.Getenv("USERNAME")); shellUser != "" {
		return shellUser
	}
	if current, err := osuser.Current(); err == nil {
		return strings.TrimSpace(current.Username)
	}
	return ""
}

func runAuthLogin(cmd *cobra.Command, hostFlag, githubLogin, email string) error {
	cfg, _ := authconfig.Load()
	host := resolveHost(hostFlag, cfg)
	resolvedGithubLogin := resolveGitHubLogin(githubLogin, host, cfg)
	if strings.TrimSpace(resolvedGithubLogin) == "" {
		return fmt.Errorf("github login not found. pass --github-login or set OPS_GITHUB_LOGIN")
	}
	client := backendclient.New(host, "")
	ctx := commandContext(cmd)

	start, err := client.StartDeviceLogin(ctx, dto.DeviceStartRequest{
		GithubLogin: resolvedGithubLogin,
		Email:       stringPtrIfNotEmpty(email),
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Open %s and enter code %s\n", start.VerificationURI, start.UserCode); err != nil {
		return err
	}

	poll, err := client.PollDeviceLogin(ctx, dto.DevicePollRequest{PollToken: start.PollToken})
	if err != nil {
		return err
	}

	cfg, _ = authconfig.Load()
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	cfg.UpsertTokenForHost(authconfig.Token{
		Host:            host,
		GitHubLogin:     poll.GithubLogin,
		DefaultTeamSlug: poll.DefaultTeamSlug,
		BearerToken:     poll.BearerToken,
		IssuedAt:        poll.IssuedAt,
	})
	if err := authconfig.Save(cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %s on %s\n", poll.GithubLogin, host)
	return err
}

func isLocalHost(host string) bool {
	parsed, err := url.Parse(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	name := strings.ToLower(parsed.Hostname())
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}
