package cli

import (
	"os"
	"testing"

	"github.com/winshare/zeroops/internal/shared/authconfig"
)

func TestResolveHostUsesDotEnvPort(t *testing.T) {
	t.Setenv("OPS_HOST", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(".env", []byte("OPS_HOST_PORT=18080\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.env) error = %v", err)
	}

	got := resolveHost("", authconfig.File{})
	if got != "http://127.0.0.1:18080" {
		t.Fatalf("resolveHost() = %q, want %q", got, "http://127.0.0.1:18080")
	}
}
