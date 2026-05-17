package createapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var appSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

var reservedSlugs = map[string]struct{}{
	"system": {},
	"api":    {},
	"auth":   {},
	"v1":     {},
	"me":     {},
}

func validateSlug(slug string) error {
	slug = strings.TrimSpace(slug)
	if !appSlugPattern.MatchString(slug) {
		return fmt.Errorf("invalid slug")
	}
	if _, ok := reservedSlugs[slug]; ok {
		return fmt.Errorf("reserved slug")
	}
	return nil
}

var (
	githubHTTPS = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+$`)
	githubSSH   = regexp.MustCompile(`^git@github\.com:[^/]+/[^/]+\.git$`)
)

// ErrLocalFileRepoDisabled is returned when a file:// repo_url is supplied
// but LOCAL_FILE_REPO_ENABLED is not "true" (ADR-0012 § 3.1).
var ErrLocalFileRepoDisabled = errors.New("local file repo disabled")

// ErrRepoPathInvalid is returned when a file:// path fails the
// LOCAL_FILE_REPO_ROOT whitelist (ADR-0012 § 3.4 path safety).
var ErrRepoPathInvalid = errors.New("repo path invalid")

// ErrRepoPathNotFound is returned when a file:// path does not exist on
// disk (or cannot be resolved through symlinks).
var ErrRepoPathNotFound = errors.New("repo path not found")

func validateRepoURL(repoURL string) error {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if strings.HasPrefix(repoURL, "file://") {
		return validateLocalRepoURL(repoURL)
	}
	if !githubHTTPS.MatchString(repoURL) && !githubSSH.MatchString(repoURL) {
		return fmt.Errorf("invalid repo_url")
	}
	return nil
}

// validateLocalRepoURL enforces the file:// path safety rules from
// ADR-0012 § 3.4: env gate, absolute path, in-root after symlink
// resolution, exists on disk.
func validateLocalRepoURL(repoURL string) error {
	if !localFileRepoEnabled() {
		return ErrLocalFileRepoDisabled
	}
	raw := strings.TrimPrefix(repoURL, "file://")
	if !filepath.IsAbs(raw) {
		return ErrRepoPathInvalid
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return ErrRepoPathNotFound
	}
	root := strings.TrimSpace(os.Getenv("LOCAL_FILE_REPO_ROOT"))
	if root == "" {
		return ErrRepoPathInvalid
	}
	rootResolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return ErrRepoPathInvalid
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrRepoPathInvalid
	}
	return nil
}

func localFileRepoEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LOCAL_FILE_REPO_ENABLED")), "true")
}
