package db

import "time"

type Team struct {
	ID         string
	Slug       string
	Name       string
	Plan       string
	ArchivedAt *time.Time
}

type TeamMembership struct {
	Team      Team
	UserID    string
	Role      string
	JoinedAt  *time.Time
	InvitedAt *time.Time
}
