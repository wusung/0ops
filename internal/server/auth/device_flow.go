package auth

import (
	"context"
	"errors"
	"time"
)

// DeviceFlowRequest is the GitHub OAuth2 device flow request
// https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app#http-request
type DeviceFlowRequest struct {
	ClientID string `json:"client_id"`
	Scopes   string `json:"scopes"`
}

// DeviceFlowResponse is the initial response when requesting a device code
type DeviceFlowResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DevicePollRequest is the request to poll for authorization
type DevicePollRequest struct {
	PollToken string `json:"poll_token"`
}

// DevicePollResponse is the response when user has authorized
type DevicePollResponse struct {
	AccessToken      string      `json:"access_token"`
	TokenType        string      `json:"token_type"`
	ExpiresIn        int         `json:"expires_in"`
	Team             TeamInfo    `json:"team"`
	AvailableTools   []ToolGrant `json:"available_tools"`
	NextStep         string      `json:"next_step"`
	TemporaryTokenID string      `json:"temporary_token_id,omitempty"`
}

// TeamInfo holds basic team information
type TeamInfo struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ToolGrant represents an MCP tool that can be granted
type ToolGrant struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Description    string `json:"description"`
	DefaultAllowed bool   `json:"default_allowed"`
	RiskLevel      string `json:"risk_level,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

// DeviceFlowStore defines the database operations needed for device flow
type DeviceFlowStore interface {
	// Device flow operations
	CreateDeviceCode(ctx context.Context, deviceCode, userCode string, expiresAt time.Time) (pollToken string, err error)
	GetDeviceCodeByPollToken(ctx context.Context, pollToken string) (deviceCode string, expiresAt time.Time, err error)
	DeleteDeviceCode(ctx context.Context, pollToken string) error

	// Temp token operations for tool grants selection
	CreateTempToken(ctx context.Context, userID, teamID string, expiresAt time.Time) (tempTokenID string, err error)
	GetTempToken(ctx context.Context, tempTokenID string) (userID, teamID string, expiresAt time.Time, err error)
	DeleteTempToken(ctx context.Context, tempTokenID string) error
}

// DeviceFlowManager handles OAuth2 device flow operations
type DeviceFlowManager struct {
	store          DeviceFlowStore
	clientID       string
	clientSecret   string
	githubEndpoint string
}

// NewDeviceFlowManager creates a new device flow manager
func NewDeviceFlowManager(store DeviceFlowStore, clientID, clientSecret string) *DeviceFlowManager {
	return &DeviceFlowManager{
		store:          store,
		clientID:       clientID,
		clientSecret:   clientSecret,
		githubEndpoint: "https://github.com",
	}
}

// StartFlow initiates GitHub device flow
func (m *DeviceFlowManager) StartFlow(ctx context.Context) (DeviceFlowResponse, error) {
	// TODO: Implement actual GitHub device flow request
	// For now, return error to indicate not yet implemented
	return DeviceFlowResponse{}, errors.New("device flow not yet fully implemented")
}

// PollAuthorization polls GitHub for authorization status
func (m *DeviceFlowManager) PollAuthorization(ctx context.Context, pollToken string) (DevicePollResponse, error) {
	// TODO: Implement actual GitHub authorization polling
	return DevicePollResponse{}, errors.New("device flow not yet fully implemented")
}

// AvailableTools returns the list of all available MCP tools
func AvailableTools() []ToolGrant {
	return []ToolGrant{
		// Read-only tools (auto-selected)
		{
			ID:             "list_apps",
			Name:           "List Apps",
			Category:       "read",
			Description:    "列出該 team 的所有 app",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "get_app",
			Name:           "Get App",
			Category:       "read",
			Description:    "取得 app 詳細資訊",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "list_domains",
			Name:           "List Domains",
			Category:       "read",
			Description:    "列出 app 的所有 domains",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "get_deploy_status",
			Name:           "Get Deploy Status",
			Category:       "read",
			Description:    "取得 deployment 狀態",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "tail_logs",
			Name:           "Tail Logs",
			Category:       "read",
			Description:    "即時查看 app 日誌",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "inspect_repo",
			Name:           "Inspect Repo",
			Category:       "read",
			Description:    "檢查 repository 配置",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "list_teams",
			Name:           "List Teams",
			Category:       "read",
			Description:    "列出用戶的所有 teams",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},
		{
			ID:             "query_audit_log",
			Name:           "Query Audit Log",
			Category:       "read",
			Description:    "查詢 audit log",
			DefaultAllowed: true,
			RiskLevel:      "low",
		},

		// Write tools (opt-in)
		{
			ID:             "create_app",
			Name:           "Create App",
			Category:       "write",
			Description:    "建立新 app",
			DefaultAllowed: false,
			RiskLevel:      "medium",
		},
		{
			ID:             "add_domain",
			Name:           "Add Domain",
			Category:       "write",
			Description:    "新增 domain",
			DefaultAllowed: false,
			RiskLevel:      "medium",
		},
		{
			ID:             "redeploy",
			Name:           "Redeploy",
			Category:       "write",
			Description:    "重新部署 app",
			DefaultAllowed: false,
			RiskLevel:      "medium",
		},
		{
			ID:             "update_app",
			Name:           "Update App",
			Category:       "write",
			Description:    "更新 app 設置",
			DefaultAllowed: false,
			RiskLevel:      "medium",
		},
		{
			ID:             "invite_member",
			Name:           "Invite Member",
			Category:       "write",
			Description:    "邀請 team 成員",
			DefaultAllowed: false,
			RiskLevel:      "medium",
		},
		{
			ID:             "update_app_settings",
			Name:           "Update App Settings",
			Category:       "write",
			Description:    "更新 app 的進階設置",
			DefaultAllowed: false,
			RiskLevel:      "medium",
		},

		// Delete/Destructive tools (high risk, opt-in)
		{
			ID:             "delete_app",
			Name:           "Delete App",
			Category:       "delete",
			Description:    "刪除 app 與相關資源",
			DefaultAllowed: false,
			RiskLevel:      "high",
			Warning:        "⚠️ 無法復原",
		},
		{
			ID:             "remove_domain",
			Name:           "Remove Domain",
			Category:       "delete",
			Description:    "移除 domain",
			DefaultAllowed: false,
			RiskLevel:      "high",
			Warning:        "⚠️ 無法復原",
		},
		{
			ID:             "uninstall_github_app",
			Name:           "Uninstall GitHub App",
			Category:       "delete",
			Description:    "卸載 GitHub App 整合",
			DefaultAllowed: false,
			RiskLevel:      "high",
			Warning:        "⚠️ 無法復原",
		},

		// Sensitive tools (high risk, opt-in)
		{
			ID:             "rotate_secrets",
			Name:           "Rotate Secrets",
			Category:       "sensitive",
			Description:    "輪轉 secrets",
			DefaultAllowed: false,
			RiskLevel:      "high",
			Warning:        "⚠️ 謹慎使用",
		},
		{
			ID:             "view_secrets",
			Name:           "View Secrets",
			Category:       "sensitive",
			Description:    "檢視 secrets",
			DefaultAllowed: false,
			RiskLevel:      "high",
			Warning:        "⚠️ 謹慎使用",
		},
		{
			ID:             "github_install",
			Name:           "GitHub Install",
			Category:       "sensitive",
			Description:    "GitHub App 安裝設置",
			DefaultAllowed: false,
			RiskLevel:      "high",
			Warning:        "⚠️ 謹慎使用",
		},
	}
}

// ValidateToolGrants validates that requested tools exist
func ValidateToolGrants(requestedTools []string) error {
	available := make(map[string]bool)
	for _, tool := range AvailableTools() {
		available[tool.ID] = true
	}

	for _, tool := range requestedTools {
		if !available[tool] {
			return errors.New("invalid tool: " + tool)
		}
	}
	return nil
}

// FilterToolsByDefault returns tools filtered by their default_allowed status
func FilterToolsByDefault(allowDefault bool) []ToolGrant {
	var result []ToolGrant
	for _, tool := range AvailableTools() {
		if tool.DefaultAllowed == allowDefault {
			result = append(result, tool)
		}
	}
	return result
}
