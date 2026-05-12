package dto

import "time"

type DeviceStartRequest struct {
	GithubLogin string  `json:"github_login"`
	Email       *string `json:"email,omitempty"`
}

type DeviceStartResponse struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	PollToken       string `json:"poll_token"`
	IntervalSeconds int    `json:"interval_s"`
	TTLSeconds      int    `json:"ttl_s"`
}

type DevicePollRequest struct {
	PollToken string `json:"poll_token"`
}

type DevicePollResponse struct {
	BearerToken     string    `json:"bearer_token"`
	DefaultTeamSlug string    `json:"default_team_slug"`
	GithubLogin     string    `json:"github_login"`
	IssuedAt        time.Time `json:"issued_at"`
}

type DeviceCallbackRequest struct {
UserCode    string `json:"user_code"`
AccessToken string `json:"access_token"`
}

type DeviceCallbackResponse struct {
Status string `json:"status"`
}

type DevicePollPendingResponse struct {
	Status string `json:"status"`
}

type PATCreateRequest struct {
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	ExpiresDays int      `json:"expires_days"`
}

type PATCreateResponse struct {
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PATListResponse struct {
	Items []PATListItem `json:"items"`
}

type PATListItem struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}
