package createapp

import "context"

// GitHubInspector preserves the pre-sub-spec behaviour: returns an empty
// RepoMetadata. The real GitHub App + paketo detect call is tracked
// separately under inspect_repo handler refactor (see spec §5.1 step 4
// and ADR-0012 § 7 Revisit Triggers).
type GitHubInspector struct{}

func (GitHubInspector) Inspect(_ context.Context, _ string, _ string) (RepoMetadata, error) {
	return RepoMetadata{GitHubAppStatus: "not_applicable"}, nil
}
