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

// AdminRetryDeleteRequest re-drives a stuck app delete (status='deleting' with
// a failed_permanently cleanup_residue job). Admin-only recovery action.
//
//nolint:revive // exported for public API
type AdminRetryDeleteRequest struct {
	TeamSlug string `json:"team_slug"`
	AppSlug  string `json:"app_slug"`
}

// AdminRetryDeleteResponse returns the freshly enqueued cleanup_residue job id.
//
//nolint:revive // exported for public API
type AdminRetryDeleteResponse struct {
	JobID   string `json:"job_id"`
	AppSlug string `json:"app_slug"`
	Status  string `json:"status"`
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

// SideEffect is one entry in PreviewResponse.SideEffects. delete_app
// populates these with the 5 ordered effects in spec § 4.3; other actions
// may leave the slice empty.
//
//nolint:revive // exported for public API
type SideEffect struct {
	Effect      string `json:"effect"`
	Reversible  bool   `json:"reversible"`
	Description string `json:"description"`
}

//nolint:revive // exported for public API
type PreviewResponse struct {
	PreviewID    string       `json:"preview_id"`
	Action       string       `json:"action"`
	Summary      string       `json:"summary"`
	ExpiresAt    time.Time    `json:"expires_at"`
	SideEffects  []SideEffect `json:"side_effects,omitempty"`
}

//nolint:revive // exported for public API
type ConfirmInviteMemberRequest struct {
	PreviewID string `json:"preview_id"`
}

//nolint:revive // exported for public API
type ConfirmRemoveMemberRequest struct {
	PreviewID string `json:"preview_id"`
}
