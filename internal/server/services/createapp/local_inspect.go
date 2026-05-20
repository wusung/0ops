package createapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalInspector inspects a file:// repo by reading the local filesystem and
// invoking `git` for HEAD and default branch. Paketo language detection is a
// minimal heuristic that mirrors sub-spec § 5.3.
type LocalInspector struct{}

// ErrBuildpackDetectFailed is returned when paketo cannot identify a stack
// from the on-disk files (no package.json / go.mod / requirements.txt / ...).
var ErrBuildpackDetectFailed = errors.New("buildpack_detect_failed")

// ErrRepoNotGit is returned when the path exists but is not a git repository.
var ErrRepoNotGit = errors.New("repo_not_git")

func (LocalInspector) Inspect(ctx context.Context, repoURL, _ string) (RepoMetadata, error) {
	if err := validateLocalRepoURL(repoURL); err != nil {
		return RepoMetadata{}, err
	}
	path := strings.TrimPrefix(repoURL, "file://")
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return RepoMetadata{}, ErrRepoPathNotFound
	}
	if _, err := os.Stat(filepath.Join(resolved, ".git")); err != nil {
		return RepoMetadata{}, ErrRepoNotGit
	}
	commit, err := runGit(ctx, resolved, "rev-parse", "HEAD")
	if err != nil {
		return RepoMetadata{}, fmt.Errorf("git rev-parse: %w", err)
	}
	branch, err := runGit(ctx, resolved, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "HEAD" {
		branch = "main"
	}
	builder, port, ok := detectPaketo(resolved)
	if !ok {
		return RepoMetadata{}, ErrBuildpackDetectFailed
	}
	return RepoMetadata{
		CommitSHA:       commit,
		DefaultBranch:   branch,
		Builder:         builder,
		PrimaryPort:     port,
		GitHubAppStatus: "not_applicable",
	}, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// detectPaketo runs a minimal language heuristic. The matrix expansion plan
// is tracked in ADR-0012 § 9 Open Q #5; v1 covers Node / Go / Python only.
// NOTE: keep the marker list in sync with detectPaketoUpload in
// upload_inspect.go. If a new language is added, edit both functions.
func detectPaketo(dir string) (string, int, bool) {
	switch {
	case fileExists(dir, "package.json"):
		return "paketobuildpacks/builder-jammy-base", 3000, true
	case fileExists(dir, "go.mod"):
		return "paketobuildpacks/builder-jammy-base", 8080, true
	case fileExists(dir, "pyproject.toml"), fileExists(dir, "requirements.txt"):
		return "paketobuildpacks/builder-jammy-base", 8000, true
	}
	return "", 0, false
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
