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
