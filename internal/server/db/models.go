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
