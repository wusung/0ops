package dto

import "time"

//nolint:revive // exported for public API
type MemberRef struct {
	UserID      string     `json:"user_id"`
	GithubLogin *string    `json:"github_login,omitempty"`
	Email       *string    `json:"email,omitempty"`
	Role        string     `json:"role"`
	InvitedAt   *time.Time `json:"invited_at,omitempty"`
	JoinedAt    *time.Time `json:"joined_at,omitempty"`
}

//nolint:revive // exported for public API
type ListMembersResponse struct {
	Items []MemberRef `json:"items"`
}

//nolint:revive // exported for public API
type BootstrapOwnerRequest struct {
	TeamSlug    string  `json:"team_slug"`
	TeamName    string  `json:"team_name"`
	GithubLogin string  `json:"github_login"`
	Email       *string `json:"email,omitempty"`
}

//nolint:revive // exported for public API
type BootstrapOwnerResponse struct {
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
}

//nolint:revive // exported for public API
type InviteMemberRequest struct {
	GithubLogin *string `json:"github_login,omitempty"`
	Email       *string `json:"email,omitempty"`
	Role        string  `json:"role"`
}

//nolint:revive // exported for public API
type InviteMemberResponse struct {
	Member MemberRef `json:"member"`
}

//nolint:revive // exported for public API
type RemoveMemberRequest struct {
	UserID string `json:"user_id"`
}

//nolint:revive // exported for public API
type PreviewResponse struct {
	PreviewID string    `json:"preview_id"`
	Action    string    `json:"action"`
	Summary   string    `json:"summary"`
	ExpiresAt time.Time `json:"expires_at"`
}

//nolint:revive // exported for public API
type ConfirmInviteMemberRequest struct {
	PreviewID string `json:"preview_id"`
}

//nolint:revive // exported for public API
type ConfirmRemoveMemberRequest struct {
	PreviewID string `json:"preview_id"`
}
