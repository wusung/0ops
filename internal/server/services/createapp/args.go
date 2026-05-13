package createapp

import (
	"fmt"
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

func validateRepoURL(repoURL string) error {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	githubHTTPS := regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+$`)
	githubSSH := regexp.MustCompile(`^git@github\.com:[^/]+/[^/]+\.git$`)
	if !githubHTTPS.MatchString(repoURL) && !githubSSH.MatchString(repoURL) {
		return fmt.Errorf("invalid repo_url")
	}
	return nil
}
