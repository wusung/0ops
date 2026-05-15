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

// AppCreateRequest is the create_app preview payload.
type AppCreateRequest struct {
	Slug    string  `json:"slug"`
	RepoURL string  `json:"repo_url"`
	Ref     string  `json:"ref"`
	Builder *string `json:"builder,omitempty"`
}

// ConfirmCreateAppRequest confirms create_app with a preview id.
type ConfirmCreateAppRequest struct {
	PreviewID string `json:"preview_id"`
}

// AppCreateResponse is the create_app confirmation result.
type AppCreateResponse struct {
	AppID         string `json:"app_id"`
	AppSlug       string `json:"app_slug"`
	DeployRunID   string `json:"deploy_run_id"`
	TraceID       string `json:"trace_id"`
	SubdomainURL  string `json:"subdomain_url"`
	InitialDeploy bool   `json:"initial_deploy"`
}

// AppDeleteRequest is the delete_app preview payload. Confirm must equal
// the path-derived app_slug as a typed-confirmation guard against
// accidental deletion (delete-app-flow spec § 4.1 hard rule #3).
type AppDeleteRequest struct {
	Confirm string `json:"confirm"`
}

// ConfirmDeleteAppRequest confirms delete_app with a preview id.
type ConfirmDeleteAppRequest struct {
	PreviewID string `json:"preview_id"`
}

// AppDeleteResponse is the delete_app confirmation result. DeletedAt is
// the timestamp when the app transitioned to status='deleting'; the row
// itself is hard-deleted asynchronously by the cleanup_residue reconciler
// once ArgoCD prune completes (spec § 5.3, § 6.2).
type AppDeleteResponse struct {
	AppID     string `json:"app_id"`
	AppSlug   string `json:"app_slug"`
	DeletedAt string `json:"deleted_at"`
	TraceID   string `json:"trace_id"`
}
