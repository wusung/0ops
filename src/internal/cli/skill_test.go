package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wusung/0ops/internal/cli/skillasset"
)

// TestEmbeddedSkillMatchesRepoSkill keeps the embedded copy byte-identical to
// the canonical repo-level skill, so `0ops skill install` can never ship an
// older contract than the one skill_contract_test.go asserts against.
// spec: docs/features/agent-skill/skill-distribution-spec.md § 3
func TestEmbeddedSkillMatchesRepoSkill(t *testing.T) {
	want, err := os.ReadFile(filepath.Clean(skillPath))
	if err != nil {
		t.Fatalf("read %s: %v", skillPath, err)
	}
	if got := skillasset.Markdown(); got != string(want) {
		t.Fatalf("embedded skill differs from %s — run `./manage.sh skill-sync`", skillPath)
	}
}

func runSkillInstall(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newSkillCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"install"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("skill install %v: %v (output: %s)", args, err, out.String())
	}
	return out.String()
}

// TestSkillInstallUserLevel covers the default target: a clean HOME gets the
// skill at ~/.claude/skills/0ops/SKILL.md (spec § 7.1).
func TestSkillInstallUserLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := runSkillInstall(t)
	target := filepath.Join(home, ".claude", "skills", "0ops", "SKILL.md")

	if !strings.Contains(out, "installed") {
		t.Errorf("expected an install line, got %q", out)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	if string(got) != skillasset.Markdown() {
		t.Error("installed skill does not match the embedded one")
	}
}

// TestSkillInstallIdempotent: a second run must not rewrite or back up
// anything (spec § 7.2).
func TestSkillInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	runSkillInstall(t)
	out := runSkillInstall(t)

	if !strings.Contains(out, "already up-to-date") {
		t.Errorf("expected already up-to-date on re-run, got %q", out)
	}
	dir := filepath.Join(home, ".claude", "skills", "0ops")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Errorf("idempotent re-run left a backup: %s", e.Name())
		}
	}
}

// TestSkillInstallBacksUpDivergedFile: a hand-edited target is preserved in a
// timestamped backup and replaced by the canonical content (spec § 7.3).
func TestSkillInstallBacksUpDivergedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".claude", "skills", "0ops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	target := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(target, []byte("hand-edited\n"), 0o600); err != nil {
		t.Fatalf("seed %s: %v", target, err)
	}

	out := runSkillInstall(t)
	if !strings.Contains(out, "backed up") {
		t.Errorf("expected a backup line, got %q", out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	if string(got) != skillasset.Markdown() {
		t.Error("target was not replaced with the canonical skill")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var backups int
	for _, e := range entries {
		if !strings.Contains(e.Name(), ".bak.") {
			continue
		}
		backups++
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read backup %s: %v", e.Name(), err)
		}
		if string(data) != "hand-edited\n" {
			t.Errorf("backup %s lost the previous content: %q", e.Name(), data)
		}
	}
	if backups != 1 {
		t.Errorf("expected exactly 1 backup, got %d", backups)
	}
}

// TestSkillInstallProjectLevel: --project writes into the given directory and
// leaves HOME alone (spec § 7.4).
func TestSkillInstallProjectLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()

	runSkillInstall(t, "--project", project)

	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "0ops", "SKILL.md")); err != nil {
		t.Fatalf("project skill not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("--project must not touch HOME, got err=%v", err)
	}
}

// TestSkillInstallPrintOnly writes nothing (spec § 7.5).
func TestSkillInstallPrintOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := runSkillInstall(t, "--print-only")

	if !strings.Contains(out, "name: 0ops") {
		t.Errorf("expected the skill body in the output, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("--print-only must not write, got err=%v", err)
	}
}

// TestOnboardInstallsSkill: onboard wires the skill without a backend, so the
// installer one-liner leaves the user's agent able to find 0ops (spec § 7.6).
func TestOnboardInstallsSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cmd := newOnboardCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"https://api.example.com", "--skip-login"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("onboard: %v (output: %s)", err, out.String())
	}

	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "0ops", "SKILL.md")); err != nil {
		t.Fatalf("onboard did not install the skill: %v", err)
	}
}

// TestOnboardSkipSkill honours the opt-out (spec § 5).
func TestOnboardSkipSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cmd := newOnboardCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"https://api.example.com", "--skip-login", "--skip-skill"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("onboard: %v (output: %s)", err, out.String())
	}

	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("--skip-skill must not write, got err=%v", err)
	}
}
