package localbuild

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// RepoURLStore is the minimal store contract RepoRootLookup needs.
type RepoURLStore interface {
	GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error)
}

// RepoRootLookup resolves a stored repo_url to a local filesystem path and
// re-validates it against the configured root. This closes the TOCTOU window
// between preview-time validation (createapp.validateLocalRepoURL) and
// confirm-time dispatch: the dispatcher is an independent trust boundary.
type RepoRootLookup struct {
	Store RepoURLStore
	Root  string
}

func (l RepoRootLookup) ResolveLocalPath(ctx context.Context, teamSlug, appSlug string) (string, string, error) {
	url, err := l.Store.GetAppRepoURLByTeamAndAppSlug(ctx, teamSlug, appSlug)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(url, "file://") {
		return "", "", errors.New("repo_url not file://")
	}
	path := strings.TrimPrefix(url, "file://")
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", err
	}
	rootResolved, err := filepath.EvalSymlinks(filepath.Clean(l.Root))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("repo path escapes root")
	}
	// node-demo (the M5.6 e2e fixture) ships package.json so jammy-base is
	// the right builder. Multi-language matrix expansion tracked in
	// ADR-0012 § 9 Open Q #5.
	return resolved, "paketobuildpacks/builder-jammy-base", nil
}
