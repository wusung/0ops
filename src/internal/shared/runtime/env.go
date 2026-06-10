// Package runtime exposes process-wide environment kind detection and
// production safety assertions. Introduced by ADR-0012 to gate dev-only
// knobs (file:// repo, local build pipeline) out of production; extended
// by ADR-0013 to require app-source ingestion env (APP_SOURCE_INGEST_ROOT,
// OPS_BUILD_TOKEN_SECRET) when running in production.
package runtime

import (
	"fmt"
	"os"
	"strings"
)

type EnvKind string

const (
	EnvDevelopment EnvKind = "development"
	EnvStaging     EnvKind = "staging"
	EnvProduction  EnvKind = "production"
)

// CurrentEnv reads OPS_ENV and returns the parsed kind. Unknown or empty
// values default to EnvDevelopment so local workflows keep working when
// the env var is unset.
func CurrentEnv() EnvKind {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("OPS_ENV")))
	switch raw {
	case string(EnvProduction):
		return EnvProduction
	case string(EnvStaging):
		return EnvStaging
	default:
		return EnvDevelopment
	}
}

// IsProduction reports whether the current process is running in production.
func IsProduction() bool {
	return CurrentEnv() == EnvProduction
}

// defaultDomainBase is the platform base domain used when OPS_DOMAIN_BASE is
// unset. Deploy layers (helm config.domainBase → OPS_DOMAIN_BASE, bootstrap
// PROD_BASE_DOMAIN) override it; switching domains requires no code change.
const defaultDomainBase = "jesontech.com"

// DomainBase returns the platform base domain that app subdomains, the
// Cloudflare wildcard route, and the domain-verify reserved suffix derive
// from. Reads OPS_DOMAIN_BASE; empty falls back to defaultDomainBase.
func DomainBase() string {
	if v := strings.TrimSpace(os.Getenv("OPS_DOMAIN_BASE")); v != "" {
		return v
	}
	return defaultDomainBase
}

// AppHostname returns the public hostname for an app slug, e.g.
// "nextdemo.jesontech.com".
func AppHostname(slug string) string {
	return slug + "." + DomainBase()
}

// AssertProductionSafe panics when:
//   - ADR-0012: LOCAL_FILE_REPO_ENABLED, LOCAL_BUILD_ENABLED, LOCAL_REGISTRY are set in production
//   - ADR-0013: APP_SOURCE_INGEST_ROOT or OPS_BUILD_TOKEN_SECRET is unset in production
//
// Called from server boot (cmd/server/main.go) to fail fast rather than allow a
// misconfigured deploy to silently expose dev-only knobs or run without an
// ingestion store / build-token secret.
func AssertProductionSafe() {
	if !IsProduction() {
		return
	}
	for _, key := range []string{
		"LOCAL_FILE_REPO_ENABLED",
		"LOCAL_BUILD_ENABLED",
	} {
		if envTrue(key) {
			panic(fmt.Sprintf("runtime: %s=true is forbidden when OPS_ENV=production (ADR-0012)", key))
		}
	}
	if strings.TrimSpace(os.Getenv("LOCAL_REGISTRY")) != "" {
		panic("runtime: LOCAL_REGISTRY must be unset when OPS_ENV=production (ADR-0012)")
	}
	for _, key := range []string{
		"APP_SOURCE_INGEST_ROOT",
		"OPS_BUILD_TOKEN_SECRET",
	} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			panic(fmt.Sprintf("runtime: %s must be set when OPS_ENV=production (ADR-0013)", key))
		}
	}
}

func envTrue(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}
