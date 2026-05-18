// Package runtime exposes process-wide environment kind detection and
// production safety assertions. Introduced by ADR-0012 to gate dev-only
// knobs (file:// repo, local build pipeline) out of production.
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

// AssertProductionSafe panics when any LOCAL_* dev knob is enabled while
// running in production. Called from server boot (cmd/server/main.go) to
// fail fast rather than allowing a misconfigured deploy to silently expose
// the file:// inspector or local build dispatcher. Per ADR-0012 § 3.1.
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
}

func envTrue(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}
