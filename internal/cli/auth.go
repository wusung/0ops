package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared/authconfig"
	"github.com/winshare/zeroops/internal/shared/backendclient"
)

// newAuthCommand returns the auth command for the 0ops CLI
func newAuthCommand() *cobra.Command {
	var (
		baseURL string
		token   string
	)

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication and MCP tool permissions",
	}

	cmd.PersistentFlags().StringVar(&baseURL, "host", "", "backend host")
	cmd.PersistentFlags().StringVar(&token, "token", "", "bearer token")

	// login subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "login",
		Short: "Authenticate with GitHub OAuth2 and set MCP tool permissions",
		Long: `Authenticate with GitHub using device flow and select which MCP tools
are allowed to be used by the CLI or MCP server.

The login flow will:
1. Start GitHub device flow authentication
2. Ask you to visit GitHub to authorize
3. Present a list of available tools to grant permission to
4. Save the access token to ~/.config/0ops/auth.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return handleAuthLogin(cmd, baseURL)
		},
	})

	// status subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show current authentication status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return handleAuthStatus(cmd, baseURL, token)
		},
	})

	// grant subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "grant <tool>",
		Short: "Grant permission for an MCP tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return handleAuthGrant(cmd, baseURL, token, args[0])
		},
	})

	// revoke subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <tool>",
		Short: "Revoke permission for an MCP tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return handleAuthRevoke(cmd, baseURL, token, args[0])
		},
	})

	return cmd
}

// handleAuthLogin performs the GitHub OAuth2 device flow login
func handleAuthLogin(cmd *cobra.Command, baseURL string) error {
	cfg, _ := authconfig.Load()
	host := resolveHost(baseURL, cfg)

	// TODO: Implement actual GitHub device flow
	// For now, provide placeholder instructions
	fmt.Fprintf(cmd.OutOrStdout(), "Device Flow Login\n")
	fmt.Fprintf(cmd.OutOrStdout(), "==================\n\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s\n\n", host)
	fmt.Fprintf(cmd.OutOrStdout(), "TODO: Implement GitHub device flow authentication\n")
	fmt.Fprintf(cmd.OutOrStdout(), "1. POST to /v1/auth/device/start\n")
	fmt.Fprintf(cmd.OutOrStdout(), "2. Display user code and verification URI\n")
	fmt.Fprintf(cmd.OutOrStdout(), "3. Poll /v1/auth/device/poll until authorized\n")
	fmt.Fprintf(cmd.OutOrStdout(), "4. Present tool grants selection UI\n")
	fmt.Fprintf(cmd.OutOrStdout(), "5. POST to /v1/teams/{team}/auth:grant-tools with selection\n")
	fmt.Fprintf(cmd.OutOrStdout(), "6. Save token to auth config\n")

	return nil
}

// handleAuthStatus displays the current authentication status
func handleAuthStatus(cmd *cobra.Command, baseURL string, token string) error {
	ctxInfo, err := resolveBackendContext(baseURL, token)
	if err != nil {
		return err
	}

	cfg, _ := authconfig.Load()

	// Get default team if set
	defaultTeam := "-"
	if team, ok := cfg.DefaultTeamForHost(ctxInfo.Host); ok {
		defaultTeam = team
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Authentication Status\n")
	fmt.Fprintf(cmd.OutOrStdout(), "====================\n\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Backend Host: %s\n", ctxInfo.Host)
	fmt.Fprintf(cmd.OutOrStdout(), "Token Status: ✓ Authenticated\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Default Team: %s\n", defaultTeam)
	fmt.Fprintf(cmd.OutOrStdout(), "\nTODO: Show granted tools from backend\n")
	fmt.Fprintf(cmd.OutOrStdout(), "TODO: Show token expiration date\n")

	return nil
}

// handleAuthGrant grants permission for an MCP tool
func handleAuthGrant(cmd *cobra.Command, baseURL string, tokenFlag string, tool string) error {
	ctxInfo, err := resolveBackendContext(baseURL, tokenFlag)
	if err != nil {
		return err
	}

	// Validate tool name format
	if strings.TrimSpace(tool) == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	_ = backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)

	fmt.Fprintf(cmd.OutOrStdout(), "Granting permission for tool: %s\n", tool)
	fmt.Fprintf(cmd.OutOrStdout(), "TODO: PATCH /v1/me/auth/tool-grants to grant tool\n")

	return nil
}

// handleAuthRevoke revokes permission for an MCP tool
func handleAuthRevoke(cmd *cobra.Command, baseURL string, tokenFlag string, tool string) error {
	ctxInfo, err := resolveBackendContext(baseURL, tokenFlag)
	if err != nil {
		return err
	}

	// Validate tool name format
	if strings.TrimSpace(tool) == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	_ = backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)

	fmt.Fprintf(cmd.OutOrStdout(), "Revoking permission for tool: %s\n", tool)
	fmt.Fprintf(cmd.OutOrStdout(), "TODO: PATCH /v1/me/auth/tool-grants to revoke tool\n")

	return nil
}
