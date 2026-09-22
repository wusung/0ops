package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// skillPath is the repo-level Claude Code skill shipped with the repo.
// spec: docs/features/agent-skill/spec.md § 7
const skillPath = "../../../.claude/skills/0ops/SKILL.md"

func readSkill(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(skillPath))
	if err != nil {
		t.Fatalf("read %s: %v", skillPath, err)
	}
	return string(data)
}

// humanOnly lists commands the skill deliberately withholds from agents:
// audit-chain verification and one-off admin/identity operations that a human
// must drive (spec § 6), plus credential and SSO management.
var humanOnly = map[string]bool{
	"admin bootstrap-owner": true,
	"admin retry-delete":    true,
	"audit verify":          true,
	"auth grant":            true,
	"auth revoke":           true,
	"auth login":            true,
	"auth logout":           true,
	"auth status":           true,
	"auth tokens create":    true,
	"auth tokens list":      true,
	"auth tokens revoke":    true,
	"sso status":            true,
	"sso deprovision":       true,
}

// TestSkillMentionsEveryCommand fails when a new CLI subcommand lands without
// either an entry in the skill or an explicit human-only classification, so a
// new capability can never silently bypass the agent contract.
func TestSkillMentionsEveryCommand(t *testing.T) {
	body := readSkill(t)

	var missing []string
	var walk func(cmd *cobra.Command, path string)
	walk = func(cmd *cobra.Command, path string) {
		for _, sub := range cmd.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			full := strings.TrimSpace(path + " " + sub.Name())
			if len(sub.Commands()) > 0 {
				walk(sub, full)
				continue
			}
			if humanOnly[full] {
				continue
			}
			if !strings.Contains(body, "0ops "+full) {
				missing = append(missing, full)
			}
		}
	}
	walk(NewRootCommand(), "")

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s does not mention: %v", skillPath, missing)
	}
}

// TestSkillOnlyReferencesRealCommands catches typos and stale commands in the
// skill: every `0ops <sub> <sub>` it names must resolve in the real command tree.
func TestSkillOnlyReferencesRealCommands(t *testing.T) {
	body := readSkill(t)
	root := NewRootCommand()

	re := regexp.MustCompile(`0ops ([a-z-]+)(?: ([a-z-]+))?(?: ([a-z-]+))?`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		var args []string
		for _, part := range m[1:] {
			if part != "" {
				args = append(args, part)
			}
		}
		if _, _, err := root.Find(args); err != nil {
			t.Errorf("%s references unknown command %q: %v", skillPath, strings.Join(args, " "), err)
		}
	}
}

// TestSkillKeepsAgentGuardrails pins the rules that replaced the MCP tool
// description lint: CLI-only commands stay off-limits and confirmation is never
// skipped on the agent's behalf (spec § 5, § 6).
func TestSkillKeepsAgentGuardrails(t *testing.T) {
	body := readSkill(t)

	for _, want := range []string{
		"audit verify",          // § 6: human-only
		"admin bootstrap-owner", // § 6: human-only
		"--dry-run",             // § 5 R1: preview first
		"preview-invite",        // § 5 R1: two-step member flow
		"teams list",            // § 5 R3: confirm the target team
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing required guardrail mention %q", skillPath, want)
		}
	}

	if strings.Contains(strings.ToLower(body), "mcp") {
		t.Errorf("%s still mentions MCP; the MCP entrypoint was removed", skillPath)
	}
}
