package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newPlan(t *testing.T, host mcpHost, configPath string) *mcpSetupPlan {
	t.Helper()
	return &mcpSetupPlan{
		host:       host,
		configPath: configPath,
		mcpBinary:  "/usr/local/bin/0ops-mcp",
		opsHost:    "https://api.example.com",
	}
}

func TestClaudeCodeFreshConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	plan := newPlan(t, hostClaudeCode, cfg)

	cmd := newMcpSetupCommand()
	cmd.SetOut(new(bytes.Buffer))
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("apply: %v", err)
	}

	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("written json invalid: %v\nraw: %s", err, raw)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatalf("missing mcpServers; got %v", got)
	}
	entry, _ := servers["0ops"].(map[string]any)
	if entry == nil {
		t.Fatalf("missing mcpServers.0ops; got %v", servers)
	}
	if entry["command"] != plan.mcpBinary {
		t.Errorf("command: got %v want %v", entry["command"], plan.mcpBinary)
	}
	env, _ := entry["env"].(map[string]any)
	if env["OPS_HOST"] != plan.opsHost {
		t.Errorf("OPS_HOST: got %v want %v", env["OPS_HOST"], plan.opsHost)
	}
}

func TestClaudeCodeMergesExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	pre := map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"other": map[string]any{"command": "/usr/bin/other"},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(cfg, data, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	plan := newPlan(t, hostClaudeCode, cfg)
	cmd := newMcpSetupCommand()
	cmd.SetOut(new(bytes.Buffer))
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("apply: %v", err)
	}

	raw, _ := os.ReadFile(cfg)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("written json invalid: %v", err)
	}
	if got["theme"] != "dark" {
		t.Errorf("preserved key lost: theme=%v", got["theme"])
	}
	servers := got["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("preserved server lost: 'other'")
	}
	if _, ok := servers["0ops"]; !ok {
		t.Errorf("0ops entry not added")
	}
}

func TestClaudeCodeIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	plan := newPlan(t, hostClaudeCode, cfg)
	cmd := newMcpSetupCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)

	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	info, _ := os.Stat(cfg)
	mtime1 := info.ModTime()

	out.Reset()
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !strings.Contains(out.String(), "already up-to-date") {
		t.Errorf("idempotent message missing; got %q", out.String())
	}
	info2, _ := os.Stat(cfg)
	if !info2.ModTime().Equal(mtime1) {
		t.Errorf("file rewritten on idempotent run (mtime changed)")
	}
}

func TestClaudeCodeRefusesBadJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	if err := os.WriteFile(cfg, []byte("{not valid"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan := newPlan(t, hostClaudeCode, cfg)
	cmd := newMcpSetupCommand()
	cmd.SetOut(new(bytes.Buffer))
	err := plan.apply(cmd, true)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse failure: %v", err)
	}
	raw, _ := os.ReadFile(cfg)
	if string(raw) != "{not valid" {
		t.Errorf("file mutated on parse error; got %q", raw)
	}
}

func TestCodexFreshConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "codex", "config.toml")
	plan := newPlan(t, hostCodex, cfg)
	cmd := newMcpSetupCommand()
	cmd.SetOut(new(bytes.Buffer))
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(raw), "[mcp_servers.0ops]") {
		t.Errorf("section header missing; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), `command = "/usr/local/bin/0ops-mcp"`) {
		t.Errorf("command line missing; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), `OPS_HOST = "https://api.example.com"`) {
		t.Errorf("OPS_HOST line missing; got:\n%s", raw)
	}
}

func TestCodexPreservesExistingTables(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	pre := `# user preferences
[ui]
theme = "dark"

[mcp_servers.other]
command = "/usr/bin/other"
`
	if err := os.WriteFile(cfg, []byte(pre), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan := newPlan(t, hostCodex, cfg)
	cmd := newMcpSetupCommand()
	cmd.SetOut(new(bytes.Buffer))
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, _ := os.ReadFile(cfg)
	got := string(raw)
	for _, must := range []string{
		"# user preferences",
		"[ui]",
		`theme = "dark"`,
		"[mcp_servers.other]",
		"[mcp_servers.0ops]",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q in output:\n%s", must, got)
		}
	}
}

func TestCodexReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	pre := `[mcp_servers.0ops]
command = "/old/path"
env = { OPS_HOST = "https://stale.example.com" }

[other]
foo = 1
`
	if err := os.WriteFile(cfg, []byte(pre), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan := newPlan(t, hostCodex, cfg)
	cmd := newMcpSetupCommand()
	cmd.SetOut(new(bytes.Buffer))
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, _ := os.ReadFile(cfg)
	got := string(raw)
	if strings.Contains(got, "/old/path") {
		t.Errorf("old command not replaced:\n%s", got)
	}
	if strings.Contains(got, "stale.example.com") {
		t.Errorf("old OPS_HOST not replaced:\n%s", got)
	}
	if !strings.Contains(got, "/usr/local/bin/0ops-mcp") {
		t.Errorf("new command missing:\n%s", got)
	}
	if !strings.Contains(got, "[other]") || !strings.Contains(got, "foo = 1") {
		t.Errorf("unrelated table lost:\n%s", got)
	}
}

func TestCodexIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	plan := newPlan(t, hostCodex, cfg)
	cmd := newMcpSetupCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)

	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("first: %v", err)
	}
	info, _ := os.Stat(cfg)
	mtime1 := info.ModTime()

	out.Reset()
	if err := plan.apply(cmd, true); err != nil {
		t.Fatalf("second: %v", err)
	}
	if !strings.Contains(out.String(), "already up-to-date") {
		t.Errorf("idempotent message missing; got %q", out.String())
	}
	info2, _ := os.Stat(cfg)
	if !info2.ModTime().Equal(mtime1) {
		t.Errorf("file rewritten on idempotent run")
	}
}

func TestResolveMcpHostKey(t *testing.T) {
	cases := []struct {
		in   string
		want mcpHost
		err  bool
	}{
		{"claude", hostClaudeCode, false},
		{"claude-code", hostClaudeCode, false},
		{"codex", hostCodex, false},
		{"copilot", hostCopilotCLI, false},
		{"copilot-cli", hostCopilotCLI, false},
		{"bogus", "", true},
	}
	for _, c := range cases {
		got, err := resolveMcpHostKey(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestPrintSnippetCopilot(t *testing.T) {
	plan := &mcpSetupPlan{
		host:      hostCopilotCLI,
		mcpBinary: "/usr/local/bin/0ops-mcp",
		opsHost:   "https://api.example.com",
	}
	var buf bytes.Buffer
	if err := plan.printSnippet(&buf); err != nil {
		t.Fatalf("printSnippet: %v", err)
	}
	for _, must := range []string{"copilot-cli.md", "0ops mcp setup claude-code"} {
		if !strings.Contains(buf.String(), must) {
			t.Errorf("missing %q in:\n%s", must, buf.String())
		}
	}
}

func TestNormalizeOpsHost(t *testing.T) {
	cases := map[string]string{
		"api.example.com":          "https://api.example.com",
		"https://api.example.com/": "https://api.example.com",
		"http://127.0.0.1:8080":    "http://127.0.0.1:8080",
		"  api.example.com  ":      "https://api.example.com",
	}
	for in, want := range cases {
		if got := normalizeOpsHost(in); got != want {
			t.Errorf("normalizeOpsHost(%q) = %q want %q", in, got, want)
		}
	}
}

func TestBackupIfExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := os.WriteFile(path, []byte("orig"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backupIfExists(path); err != nil {
		t.Fatalf("backup: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	var foundBak bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "x.json.bak.") {
			foundBak = true
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if string(data) != "orig" {
				t.Errorf("backup content mismatch: %q", data)
			}
		}
	}
	if !foundBak {
		t.Errorf("backup file not created; entries: %v", entries)
	}

	missing := filepath.Join(dir, "nope.json")
	if err := backupIfExists(missing); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup on missing file should be nil error, got %v", err)
	}
}
