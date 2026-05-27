package leader

import (
	"os"
	"strings"
	"testing"
)

func TestPodIdentityUsesPodNameEnvWithSuffix(t *testing.T) {
	t.Setenv("POD_NAME", "ops-server-7b9d-abc12")
	id := PodIdentity()
	if !strings.HasPrefix(id, "ops-server-7b9d-abc12_") {
		t.Fatalf("identity = %q, want prefix %q_", id, "ops-server-7b9d-abc12")
	}
	if len(id) != len("ops-server-7b9d-abc12_")+8 {
		t.Fatalf("identity = %q, want POD_NAME + '_' + 8-char uuid suffix", id)
	}
}

func TestPodIdentityFallsBackToHostname(t *testing.T) {
	if err := os.Unsetenv("POD_NAME"); err != nil {
		t.Fatalf("unset POD_NAME: %v", err)
	}
	id := PodIdentity()
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	if !strings.HasPrefix(id, host+"_") {
		t.Fatalf("identity = %q, want hostname prefix %q_", id, host)
	}
}

func TestPodIdentityIsUniquePerCall(t *testing.T) {
	t.Setenv("POD_NAME", "ops-server-7b9d-abc12")
	first := PodIdentity()
	second := PodIdentity()
	if first == second {
		t.Fatalf("PodIdentity must include a unique suffix per call: %q == %q", first, second)
	}
}
