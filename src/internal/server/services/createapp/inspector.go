package createapp

import (
	"context"
	"strings"
)

// RepoMetadata is the inspection result consumed by preview side_effects and
// the deploy pipeline. Mirrors the spec § 5.1 step 4 contract.
type RepoMetadata struct {
	CommitSHA       string
	DefaultBranch   string
	Builder         string
	PrimaryPort     int
	GitHubAppStatus string // installed | installed_no_access | not_applicable
}

// Inspector resolves repo metadata given a repo URL and ref. Implementations
// are scheme-specific; see github_inspector.go / local_inspect.go. Added by
// ADR-0012 § 3.2 to support file:// repos without touching the production
// GitHub App path.
type Inspector interface {
	Inspect(ctx context.Context, repoURL, ref string) (RepoMetadata, error)
}

// NewInspector returns an Inspector that dispatches to the scheme-specific
// implementation: upload:// → upload, file:// → local, otherwise → github.
// Any side may be nil; nil-github returns an empty RepoMetadata (preserves
// pre-sub-spec behaviour where inspect_repo was a stub), nil-local rejects
// file:// URLs, nil-upload returns ErrUploadInspectionUnavailable.
func NewInspector(github, local, upload Inspector) Inspector {
	return inspectorRouter{github: github, local: local, upload: upload}
}

type inspectorRouter struct {
	github Inspector
	local  Inspector
	upload Inspector
}

func (r inspectorRouter) Inspect(ctx context.Context, repoURL, ref string) (RepoMetadata, error) {
	switch {
	case strings.HasPrefix(repoURL, "upload://"):
		if r.upload == nil {
			return RepoMetadata{}, ErrUploadInspectionUnavailable
		}
		return r.upload.Inspect(ctx, repoURL, ref)
	case strings.HasPrefix(repoURL, "file://"):
		if r.local == nil {
			return RepoMetadata{}, ErrLocalFileRepoDisabled
		}
		return r.local.Inspect(ctx, repoURL, ref)
	}
	if r.github == nil {
		return RepoMetadata{GitHubAppStatus: "not_applicable"}, nil
	}
	return r.github.Inspect(ctx, repoURL, ref)
}
