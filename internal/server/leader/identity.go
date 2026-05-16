package leader

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
)

// PodIdentity returns a string suitable for use as the leader election
// holder identity. Per spec § 4.2 the identity is `<POD_NAME>_<rand8>` so
// every pod start can be told apart even when re-using the same name
// (StatefulSet, controller restart, etc.). When POD_NAME is unset the
// hostname stands in; if hostname is also unavailable the constant
// "unknown" is used so the identity never collapses to just the
// random suffix.
func PodIdentity() string {
	name := strings.TrimSpace(os.Getenv("POD_NAME"))
	if name == "" {
		host, err := os.Hostname()
		if err != nil || strings.TrimSpace(host) == "" {
			name = "unknown"
		} else {
			name = host
		}
	}
	return name + "_" + randomSuffix()
}

func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on supported platforms; if it
		// does, fall through to a deterministic suffix so PodIdentity
		// never panics. The duplicate suffix is acceptable: leader
		// election only requires identity uniqueness across concurrent
		// pods, not across history.
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}
