package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared/authconfig"
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

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\n🔐 Starting GitHub OAuth2 Device Flow\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s\n\n", host)

	// Step 1: Start device flow
	fmt.Fprintf(cmd.OutOrStdout(), "Step 1/4: Starting device flow...\n")
	deviceResp, pollToken, err := startDeviceFlow(ctx, host)
	if err != nil {
		return fmt.Errorf("failed to start device flow: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Device flow started\n\n")
	fmt.Fprintf(cmd.OutOrStdout(), "👉 Visit: %s\n", deviceResp.VerificationURI)
	fmt.Fprintf(cmd.OutOrStdout(), "📝 Enter code: %s\n", deviceResp.UserCode)
	fmt.Fprintf(cmd.OutOrStdout(), "⏰ Expires in: %d seconds\n\n", deviceResp.ExpiresIn)

	// Step 2: Poll for authorization
	fmt.Fprintf(cmd.OutOrStdout(), "Step 2/4: Waiting for authorization...\n")
	pollResp, err := pollDeviceFlow(ctx, host, pollToken, deviceResp.Interval, deviceResp.ExpiresIn)
	if err != nil {
		return fmt.Errorf("device flow authorization failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ GitHub authorization successful\n")
	fmt.Fprintf(cmd.OutOrStdout(), "✓ Team: %s (%s)\n\n", pollResp.Team.Name, pollResp.Team.Slug)

	// Step 3: Present tool selection UI
	fmt.Fprintf(cmd.OutOrStdout(), "Step 3/4: Selecting MCP tool permissions\n")
	fmt.Fprintf(cmd.OutOrStdout(), "─────────────────────────────────────\n\n")

	selectedTools := presentToolSelection(cmd, pollResp.AvailableTools)
	if len(selectedTools) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "ℹ️  No tools selected. Login cancelled.\n")
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Selected %d tools\n\n", len(selectedTools))

	// Step 4: Submit tool grants and get final token
	fmt.Fprintf(cmd.OutOrStdout(), "Step 4/4: Finalizing authorization...\n")
	accessToken, err := submitToolGrants(ctx, host, pollResp.Team.Slug, pollResp.AccessToken, selectedTools)
	if err != nil {
		return fmt.Errorf("failed to grant tools: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Tool permissions saved\n\n")

	// Save token to auth config
	cfg2, _ := authconfig.Load()
	
	// Add or update token entry
	found := false
	for i, token := range cfg2.Tokens {
		if token.Host == host {
			cfg2.Tokens[i].BearerToken = accessToken
			cfg2.Tokens[i].DefaultTeamSlug = pollResp.Team.Slug
			found = true
			break
		}
	}
	if !found {
		cfg2.Tokens = append(cfg2.Tokens, authconfig.Token{
			Host:            host,
			DefaultTeamSlug: pollResp.Team.Slug,
			BearerToken:     accessToken,
		})
	}

	if err := authconfig.Save(cfg2); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✅ Login successful!\n")
	fmt.Fprintf(cmd.OutOrStdout(), "📦 Token saved to ~/.config/0ops/auth.json\n")
	fmt.Fprintf(cmd.OutOrStdout(), "🚀 Ready to use 0ops CLI and MCP tools\n\n")

	return nil
}

// deviceFlowStartResponse is the response from POST /v1/auth/device/start
type deviceFlowStartResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// deviceFlowPollResponse is the response from POST /v1/auth/device/poll
type deviceFlowPollResponse struct {
	AccessToken    string        `json:"access_token"`
	TokenType      string        `json:"token_type"`
	ExpiresIn      int           `json:"expires_in"`
	Team           teamInfo      `json:"team"`
	AvailableTools []toolInfo    `json:"available_tools"`
	NextStep       string        `json:"next_step"`
}

type teamInfo struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type toolInfo struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	DefaultAllowed bool   `json:"default_allowed"`
	RiskLevel      string `json:"risk_level,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

// startDeviceFlow initiates the device flow
func startDeviceFlow(ctx context.Context, host string) (*deviceFlowStartResponse, string, error) {
	url := host + "/v1/auth/device/start"
	reqBody := map[string]string{}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("device flow start failed: %s", string(data))
	}

	var deviceResp deviceFlowStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return nil, "", err
	}

	return &deviceResp, deviceResp.DeviceCode, nil
}

// pollDeviceFlow polls for authorization
func pollDeviceFlow(ctx context.Context, host, pollToken string, interval, expiresIn int) (*deviceFlowPollResponse, error) {
	if interval <= 0 {
		interval = 5
	}

	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device flow expired")
		}

		time.Sleep(time.Duration(interval) * time.Second)

		url := host + "/v1/auth/device/poll"
		reqBody := map[string]string{"poll_token": pollToken}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusOK {
			var pollResp deviceFlowPollResponse
			if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
				resp.Body.Close()
				return nil, err
			}
			resp.Body.Close()
			return &pollResp, nil
		}

		if resp.StatusCode != http.StatusAccepted {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("poll failed: %s", string(data))
		}

		resp.Body.Close()
	}
}

// presentToolSelection displays an interactive tool selection menu
func presentToolSelection(cmd *cobra.Command, tools []toolInfo) []string {
	if len(tools) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No tools available\n")
		return nil
	}

	// Separate tools by category and default status
	var readTools, writeTools, dangerTools []toolInfo
	for _, tool := range tools {
		if tool.Category == "read" {
			readTools = append(readTools, tool)
		} else if strings.Contains(tool.Category, "delete") || strings.Contains(tool.Category, "sensitive") {
			dangerTools = append(dangerTools, tool)
		} else {
			writeTools = append(writeTools, tool)
		}
	}

	// Display tools with default selections
	fmt.Fprintf(cmd.OutOrStdout(), "✓ [Auto-selected] Read-only (%d)\n", len(readTools))
	for _, tool := range readTools {
		fmt.Fprintf(cmd.OutOrStdout(), "  ✓ %s - %s\n", tool.ID, tool.Description)
	}

	if len(writeTools) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\n[ ] Write operations (%d)\n", len(writeTools))
		for _, tool := range writeTools {
			fmt.Fprintf(cmd.OutOrStdout(), "  [ ] %s - %s\n", tool.ID, tool.Description)
		}
	}

	if len(dangerTools) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\n[ ] ⚠️  Dangerous operations (%d)\n", len(dangerTools))
		for _, tool := range dangerTools {
			fmt.Fprintf(cmd.OutOrStdout(), "  [ ] %s - %s", tool.ID, tool.Description)
			if tool.Warning != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " %s", tool.Warning)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n")
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nOptions:\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  (y) Confirm and save [DEFAULT]\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  (c) Cancel\n\n")

	// For now, auto-confirm with default selections (interactive menu can be enhanced)
	fmt.Fprintf(cmd.OutOrStdout(), "Input [y]: ")

	// Collect all read tools (always selected) and return
	var selected []string
	for _, tool := range readTools {
		selected = append(selected, tool.ID)
	}

	return selected
}

// submitToolGrants submits the selected tools and gets the final access token
func submitToolGrants(ctx context.Context, host, teamSlug, tempToken string, tools []string) (string, error) {
	url := host + "/v1/teams/" + teamSlug + "/auth:grant-tools"

	reqBody := map[string]interface{}{
		"tools": tools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tempToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tool grant failed: %s", string(data))
	}

	var grantResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&grantResp); err != nil {
		return "", err
	}

	if accessToken, ok := grantResp["access_token"].(string); ok {
		return accessToken, nil
	}

	return "", fmt.Errorf("no access_token in response")
}

// patchToolGrantsRequest is the request body for PATCH /v1/me/auth/tool-grants
type patchToolGrantsRequest struct {
	Grant  []string `json:"grant,omitempty"`
	Revoke []string `json:"revoke,omitempty"`
}

// patchToolGrantsResponse is the response body for PATCH /v1/me/auth/tool-grants
type patchToolGrantsResponse struct {
	GrantedTools []string `json:"granted_tools"`
	RevokedTools []string `json:"revoked_tools"`
}

// patchToolGrants updates tool grants by calling PATCH /v1/me/auth/tool-grants
func patchToolGrants(ctx context.Context, host, token string, grant, revoke []string) (*patchToolGrantsResponse, error) {
	url := host + "/v1/me/auth/tool-grants"

	reqBody := patchToolGrantsRequest{
		Grant:  grant,
		Revoke: revoke,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("patch tool grants failed: %s", string(data))
	}

	var respBody patchToolGrantsResponse
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, err
	}

	return &respBody, nil
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

	// Make PATCH request to grant tool
	resp, err := patchToolGrants(ctx, ctxInfo.Host, ctxInfo.BearerToken, []string{tool}, nil)
	if err != nil {
		return fmt.Errorf("failed to grant tool: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Granted permission for tool: %s\n", tool)
	fmt.Fprintf(cmd.OutOrStdout(), "Total granted tools: %d\n", len(resp.GrantedTools))

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

	// Make PATCH request to revoke tool
	resp, err := patchToolGrants(ctx, ctxInfo.Host, ctxInfo.BearerToken, nil, []string{tool})
	if err != nil {
		return fmt.Errorf("failed to revoke tool: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Revoked permission for tool: %s\n", tool)
	fmt.Fprintf(cmd.OutOrStdout(), "Total granted tools: %d\n", len(resp.GrantedTools))

	return nil
}
