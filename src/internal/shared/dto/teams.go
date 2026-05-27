package dto

// TeamMembership is the wire DTO for a team available to the current actor.
type TeamMembership struct {
	TeamSlug string `json:"team_slug"`
	TeamName string `json:"team_name"`
	Role     string `json:"role"`
	Plan     string `json:"plan"`
}

// ListTeamsResponse wraps the current actor's teams list.
type ListTeamsResponse struct {
	Items []TeamMembership `json:"items"`
}
