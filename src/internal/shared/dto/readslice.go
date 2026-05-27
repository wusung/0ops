package dto

import "time"

// RepoInspectResponse is repository metadata derived from app config.
type RepoInspectResponse struct {
	AppSlug           string  `json:"app_slug"`
	RepoURL           *string `json:"repo_url,omitempty"`
	RepoDefaultBranch *string `json:"repo_default_branch,omitempty"`
	Builder           *string `json:"builder,omitempty"`
}

// DeployStatusResponse is the latest deploy status for an app.
type DeployStatusResponse struct {
	DeployID     string     `json:"deploy_id"`
	AppSlug      string     `json:"app_slug"`
	Status       string     `json:"status"`
	CommitSHA    *string    `json:"commit_sha,omitempty"`
	Ref          *string    `json:"ref,omitempty"`
	ErrorSummary *string    `json:"error_summary,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// LogLine is a deploy log line payload.
type LogLine struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
}

// TailLogsResponse wraps deploy log lines.
type TailLogsResponse struct {
	Items []LogLine `json:"items"`
}

// DomainRef is an app domain binding.
type DomainRef struct {
	Hostname   string     `json:"hostname"`
	Kind       *string    `json:"kind,omitempty"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// ListDomainsResponse wraps app domains.
type ListDomainsResponse struct {
	Items []DomainRef `json:"items"`
}
