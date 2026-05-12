package dto

import "time"

//nolint:revive // exported for public API
type DeviceStartRequest struct {
	GithubLogin string  `json:"github_login"`
	Email       *string `json:"email,omitempty"`
}

//nolint:revive // exported for public API
type DeviceStartResponse struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	PollToken       string `json:"poll_token"`
	IntervalSeconds int    `json:"interval_s"`
	TTLSeconds      int    `json:"ttl_s"`
}

//nolint:revive // exported for public API
type DevicePollRequest struct {
	PollToken string `json:"poll_token"`
}

//nolint:revive // exported for public API
type DevicePollResponse struct {
	BearerToken     string    `json:"bearer_token"`
	DefaultTeamSlug string    `json:"default_team_slug"`
	GithubLogin     string    `json:"github_login"`
	IssuedAt        time.Time `json:"issued_at"`
}

//nolint:revive // exported for public API
type DeviceCallbackRequest struct {
	UserCode    string `json:"user_code"`
	AccessToken string `json:"access_token"`
}

//nolint:revive // exported for public API
type DeviceCallbackResponse struct {
	Status string `json:"status"`
}

//nolint:revive // exported for public API
type DevicePollPendingResponse struct {
	Status string `json:"status"`
}

//nolint:revive // exported for public API
type PATCreateRequest struct {
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	ExpiresDays int      `json:"expires_days"`
}

//nolint:revive // exported for public API
type PATCreateResponse struct {
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

//nolint:revive // exported for public API
type PATListResponse struct {
	Items []PATListItem `json:"items"`
}

//nolint:revive // exported for public API
type PATListItem struct {
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}
