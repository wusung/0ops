package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectMcpHostsExplicitCSV(t *testing.T) {
	got := selectMcpHosts("claude-code,codex")
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(got), got)
	}
	if got[0] != hostClaudeCode || got[1] != hostCodex {
		t.Errorf("unexpected order: %v", got)
	}
}

func TestSelectMcpHostsRejectsBogus(t *testing.T) {
	got := selectMcpHosts("claude-code,bogus,codex")
	if len(got) != 2 {
		t.Fatalf("expected 2 (drop bogus), got %d: %v", len(got), got)
	}
}

func TestDetectClaudeCodeViaConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", tmpHome) // empty bin dir → no `claude` on PATH

	if detectClaudeCode() {
		t.Errorf("expected no detection with empty home")
	}

	cfg := filepath.Join(tmpHome, ".claude.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !detectClaudeCode() {
		t.Errorf("expected detection after creating ~/.claude.json")
	}
}

func TestDetectCodexViaConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", tmpHome)

	if detectCodex() {
		t.Errorf("expected no detection with empty home")
	}

	cfgDir := filepath.Join(tmpHome, ".codex")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if !detectCodex() {
		t.Errorf("expected detection after creating ~/.codex/config.toml")
	}
}

func TestSelectMcpHostsAutodetectEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", tmpHome)
	if got := selectMcpHosts(""); len(got) != 0 {
		t.Errorf("expected no autodetect on empty home, got %v", got)
	}
}

func TestAlreadyLoggedInFalseWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if alreadyLoggedIn("https://api.example.com") {
		t.Errorf("expected false on missing auth.json")
	}
}
