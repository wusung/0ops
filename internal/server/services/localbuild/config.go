package localbuild

import (
	"os"
	"strings"
)

// Config bundles the env knobs that switch on the dev-only local build path.
type Config struct {
	Enabled      bool
	Registry     string // e.g. localhost:5000 or registry:5000
	RepoRoot     string // e.g. /workspace/examples
	CallbackBase string // e.g. http://localhost:8080
	Secret       string // shared with production callback HMAC
}

func LoadConfig() Config {
	return Config{
		Enabled:      envTrue("LOCAL_BUILD_ENABLED"),
		Registry:     strings.TrimSpace(os.Getenv("LOCAL_REGISTRY")),
		RepoRoot:     strings.TrimSpace(os.Getenv("LOCAL_FILE_REPO_ROOT")),
		CallbackBase: strings.TrimSpace(os.Getenv("OPS_PUBLIC_BASE_URL")),
		Secret:       strings.TrimSpace(os.Getenv("OPS_CALLBACK_SECRET")),
	}
}

// IsUsable reports whether enough config is present to actually run a build.
// apps.go uses this to decide between RoutingDispatcher (dev) and the
// production-only workflowdispatch path.
func (c Config) IsUsable() bool {
	return c.Enabled && c.Registry != "" && c.RepoRoot != "" && c.Secret != ""
}

func envTrue(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}
