// Package dto provides data transfer objects.
package dto

import "time"

// AppRef is the wire DTO for a listed app.
type AppRef struct {
	ID                string    `json:"id"`
	TeamID            string    `json:"team_id"`
	Slug              string    `json:"slug"`
	Name              *string   `json:"name,omitempty"`
	RepoURL           *string   `json:"repo_url,omitempty"`
	RepoDefaultBranch *string   `json:"repo_default_branch,omitempty"`
	ImageRef          *string   `json:"image_ref,omitempty"`
	Builder           *string   `json:"builder,omitempty"`
	Status            *string   `json:"status,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ListAppsQuery describes team-scoped list parameters.
type ListAppsQuery struct {
	PageSize int     `json:"page_size"`
	Cursor   *string `json:"cursor,omitempty"`
}

// ListAppsResponse wraps a page of apps.
type ListAppsResponse struct {
	Items      []AppRef `json:"items"`
	NextCursor *string  `json:"next_cursor,omitempty"`
	PageSize   int      `json:"page_size"`
}
