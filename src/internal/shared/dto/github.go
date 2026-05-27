package dto

import "time"

// GitHubInstallResponse is the confirm-install last_result envelope returned
// to CLI/MCP callers (github-app-install-flow spec § 4.3).
type GitHubInstallResponse struct {
	InstallURL string    `json:"install_url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// GitHubUninstallResponse is the confirm-uninstall last_result envelope
// returned after the team's installation is cleared (spec § 5.1).
type GitHubUninstallResponse struct {
	Status         string `json:"status"`           // "uninstalled" or "no_install"
	PausedAppCount int64  `json:"paused_app_count"` // apps flipped to paused
}

// GitHubInstallStatusResponse is returned by the polling endpoint that CLI
// hits after redirecting the user to GitHub (spec § 4.5).
type GitHubInstallStatusResponse struct {
	Installed       bool   `json:"installed"`
	GithubInstallID *int64 `json:"github_install_id,omitempty"`
}

// GitHubConfirmRequest carries the preview id for install/uninstall confirm.
type GitHubConfirmRequest struct {
	PreviewID string `json:"preview_id"`
}
