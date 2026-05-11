package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	osuser "os/user"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

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

	cmd.AddCommand(newAuthTokensCommand())

	return cmd
}

func newAuthTokensCommand() *cobra.Command {
	var (
		teamFlag    string
		yesFlag     bool
		tokenName   string
		scopesFlag  string
		expiresFlag string
	)

	cmd := &cobra.Command{
		Use:   "tokens",
		Short: "Manage PAT tokens",
	}
	cmd.PersistentFlags().StringVar(&teamFlag, "team", "", "team slug")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List PAT tokens",
		RunE: func(cmd *cobra.Command, _ []string) error {
			hostFlag, tokenFlag, outputFmt, err := readTokensContextFlags(cmd)
			if err != nil {
				return err
			}
			ctxInfo, err := resolveAppsContext(teamFlag, hostFlag, tokenFlag)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).ListTeamTokens(commandContext(cmd), ctxInfo.TeamSlug)
			if err != nil {
				return err
			}
			return renderPATList(cmd, out, outputFmt)
		},
	})

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a PAT token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			hostFlag, tokenFlag, outputFmt, err := readTokensContextFlags(cmd)
			if err != nil {
				return err
			}
			ctxInfo, err := resolveAppsContext(teamFlag, hostFlag, tokenFlag)
			if err != nil {
				return err
			}
			scopeList, err := parseScopes(scopesFlag)
			if err != nil {
				return err
			}
			days, err := parseExpiresDays(expiresFlag)
			if err != nil {
				return err
			}
			out, err := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).CreateTeamToken(commandContext(cmd), ctxInfo.TeamSlug, dto.PATCreateRequest{
				Name:        tokenName,
				Scopes:      scopeList,
				ExpiresDays: days,
			})
			if err != nil {
				return err
			}
			return renderPATCreate(cmd, out, outputFmt)
		},
	}
	createCmd.Flags().StringVar(&tokenName, "name", "", "token name")
	createCmd.Flags().StringVar(&scopesFlag, "scopes", "", "comma separated scopes")
	createCmd.Flags().StringVar(&expiresFlag, "expires", "90d", "expiry window, e.g. 90d")
	cmd.AddCommand(createCmd)

	revokeCmd := &cobra.Command{
		Use:   "revoke <name>",
		Short: "Revoke a PAT token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostFlag, tokenFlag, _, err := readTokensContextFlags(cmd)
			if err != nil {
				return err
			}
			ctxInfo, err := resolveAppsContext(teamFlag, hostFlag, tokenFlag)
			if err != nil {
				return err
			}
			if !yesFlag {
				ok, err := confirmAction(cmd, fmt.Sprintf("Revoke token %q in team %q?", args[0], ctxInfo.TeamSlug))
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("revocation cancelled")
				}
			}
			return backendclient.New(ctxInfo.Host, ctxInfo.BearerToken).RevokeTeamToken(commandContext(cmd), ctxInfo.TeamSlug, args[0])
		},
	}
	revokeCmd.Flags().BoolVar(&yesFlag, "yes", false, "skip confirmation")
	cmd.AddCommand(revokeCmd)

	return cmd
}

func readTokensContextFlags(cmd *cobra.Command) (string, string, string, error) {
	hostFlag, err := cmd.Flags().GetString("host")
	if err != nil {
		return "", "", "", err
	}
	tokenFlag, err := cmd.Flags().GetString("token")
	if err != nil {
		return "", "", "", err
	}
	outputFmt, err := cmd.Flags().GetString("output")
	if err != nil {
		return "", "", "", err
	}
	return hostFlag, tokenFlag, outputFmt, nil
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

	pollInterval := time.Second
	if start.IntervalSeconds > 0 {
		pollInterval = time.Duration(start.IntervalSeconds) * time.Second
	}

	// Poll until authorization is complete or error occurs
	var poll dto.DevicePollResponse
	for {
		var err error
		poll, err = client.PollDeviceLogin(ctx, dto.DevicePollRequest{PollToken: start.PollToken})
		if err != nil {
			if err.Error() == "authorization_pending" {
				// Still pending, wait a bit and retry
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollInterval):
					continue
				}
			}
			return err
		}
		// Success - we have a token
		break
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

func renderPATCreate(cmd *cobra.Command, out dto.PATCreateResponse, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "table":
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "token\t%s\n", out.Token)
		fmt.Fprintf(w, "name\t%s\n", out.Name)
		fmt.Fprintf(w, "expires_at\t%s\n", out.ExpiresAt.Format(time.RFC3339))
		fmt.Fprintf(w, "scopes\t%s\n", strings.Join(out.Scopes, ","))
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func renderPATList(cmd *cobra.Command, out dto.PATListResponse, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "table":
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		for _, item := range out.Items {
			expires := "-"
			if item.ExpiresAt != nil {
				expires = item.ExpiresAt.Format(time.RFC3339)
			}
			lastUsed := "-"
			if item.LastUsedAt != nil {
				lastUsed = item.LastUsedAt.Format(time.RFC3339)
			}
			warning := ""
			if item.ExpiresAt != nil {
				remaining := time.Until(*item.ExpiresAt)
				switch {
				case remaining <= 0:
					warning = "expired"
				case remaining <= 14*24*time.Hour:
					warning = "expiring_soon"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", item.Name, strings.Join(item.Scopes, ","), item.CreatedAt.Format(time.RFC3339), lastUsed, expires, warning)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unsupported output format %q", outputFmt)
	}
}

func parseScopes(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("scopes are required")
	}
	return out, nil
}

func parseExpiresDays(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 90, nil
	}
	raw = strings.TrimSuffix(raw, "d")
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		return 0, fmt.Errorf("invalid expires value %q", raw)
	}
	return days, nil
}

func confirmAction(cmd *cobra.Command, question string) (bool, error) {
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	if err != nil {
		return false, err
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

func isLocalHost(host string) bool {
	parsed, err := url.Parse(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	name := strings.ToLower(parsed.Hostname())
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}
