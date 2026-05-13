package createapp

import (
	"fmt"
	"regexp"
)

type AppCreateArgs struct {
	Slug       string
	RepoURL    string
	Ref        string
	Builder    string
	DomainName string
}

var slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func (a *AppCreateArgs) Validate() error {
	if a.Slug == "" {
		return fmt.Errorf("slug required")
	}
	if !slugRegex.MatchString(a.Slug) {
		return fmt.Errorf("slug format invalid: must be lowercase alphanumeric with hyphens")
	}
	if a.RepoURL == "" {
		return fmt.Errorf("repo_url required")
	}
	if a.Ref == "" {
		return fmt.Errorf("ref required")
	}
	if a.Builder == "" {
		return fmt.Errorf("builder required")
	}
	if a.DomainName == "" {
		return fmt.Errorf("domain_name required")
	}
	return nil
}
