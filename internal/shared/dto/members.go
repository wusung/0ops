package dto

import "time"

type MemberRef struct {
	UserID      string     `json:"user_id"`
	GithubLogin *string    `json:"github_login,omitempty"`
	Email       *string    `json:"email,omitempty"`
	Role        string     `json:"role"`
	InvitedAt   *time.Time `json:"invited_at,omitempty"`
	JoinedAt    *time.Time `json:"joined_at,omitempty"`
}

type ListMembersResponse struct {
	Items []MemberRef `json:"items"`
}

type BootstrapOwnerRequest struct {
	TeamSlug    string  `json:"team_slug"`
	TeamName    string  `json:"team_name"`
	GithubLogin string  `json:"github_login"`
	Email       *string `json:"email,omitempty"`
}

type BootstrapOwnerResponse struct {
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
}

type InviteMemberRequest struct {
	GithubLogin *string `json:"github_login,omitempty"`
	Email       *string `json:"email,omitempty"`
	Role        string  `json:"role"`
}

type InviteMemberResponse struct {
	Member MemberRef `json:"member"`
}

type RemoveMemberRequest struct {
	UserID string `json:"user_id"`
}

type PreviewResponse struct {
	PreviewID string    `json:"preview_id"`
	Action    string    `json:"action"`
	Summary   string    `json:"summary"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ConfirmInviteMemberRequest struct {
	PreviewID string `json:"preview_id"`
}

type ConfirmRemoveMemberRequest struct {
	PreviewID string `json:"preview_id"`
}
