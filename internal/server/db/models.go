// Package db provides repository models mapped from sqlc rows.
package db

import "time"

// Team describes a team record.
type Team struct {
	ID         string
	Slug       string
	Name       string
	Plan       string
	ArchivedAt *time.Time
}

// TeamMembership describes a user's membership in a team.
type TeamMembership struct {
	Team      Team
	UserID    string
	Role      string
	JoinedAt  *time.Time
	InvitedAt *time.Time
}

// ToolGrant describes a tool grant for a user on a team.
type ToolGrant struct {
	ToolID  string
	Allowed bool
}

// DomainBinding describes a domain binding for an app in a team.
type DomainBinding struct {
	ID         string
	TeamID     string
	AppID      string
	AppSlug    string
	Hostname   string
	Kind       *string
	Verified   bool
	ExpiresAt  *time.Time
	VerifiedAt *time.Time
}

//nolint:revive // exported for public API
type DeployLogLine struct {
	Timestamp time.Time
	Message   string
}

//nolint:revive // exported for public API
type DeployRun struct {
	ID           string
	TeamID       string
	AppID        string
	AppSlug      string
	Status       string
	CommitSHA    *string
	Ref          *string
	ErrorSummary *string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	LogLines     []DeployLogLine
}
