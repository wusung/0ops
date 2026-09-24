// Package cli — skill subcommand.
// spec: docs/features/agent-skill/skill-distribution-spec.md
//
// 0ops skill install 把編進 binary 的 SKILL.md 寫到使用者端，讓 agent 認得
// 0ops。預設寫使用者層（~/.claude/skills/0ops/），--project 寫專案層。
package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/wusung/0ops/internal/cli/skillasset"
)

// skillRelPath is where Claude Code looks for a skill, under either the user's
// ~/.claude or a project's .claude.
var skillRelPath = filepath.Join(".claude", "skills", "0ops", "SKILL.md")

func newSkillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the 0ops Claude Code skill",
	}
	cmd.AddCommand(newSkillInstallCommand())
	return cmd
}

func newSkillInstallCommand() *cobra.Command {
	var (
		projectDir string
		printOnly  bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the 0ops skill so your AI CLI knows these commands",
		Long: `Writes the skill shipped inside this binary to disk.

Default target is the user level (~/.claude/skills/0ops/SKILL.md), so every
repo on this machine picks it up. Use --project to write it into one repo
instead. Re-running is a no-op when the target already matches; a differing
target is backed up before it is replaced.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := skillasset.Markdown()
			out := cmd.OutOrStdout()

			target, err := skillTargetPath(cmd, projectDir)
			if err != nil {
				return err
			}

			if printOnly {
				fmt.Fprintf(out, "# target: %s\n", target)
				fmt.Fprint(out, body)
				return nil
			}

			return writeSkill(out, target, body)
		},
	}
	cmd.Flags().StringVar(&projectDir, "project", ".", "install into this project directory instead of the user level")
	cmd.Flags().BoolVar(&printOnly, "print-only", false, "print the skill and its target path without writing")
	return cmd
}

// skillTargetPath resolves where the skill should land. --project selects the
// project level; otherwise the user level under $HOME.
func skillTargetPath(cmd *cobra.Command, projectDir string) (string, error) {
	if cmd.Flags().Changed("project") {
		base, err := filepath.Abs(projectDir)
		if err != nil {
			return "", fmt.Errorf("resolve --project %q: %w", projectDir, err)
		}
		return filepath.Join(base, skillRelPath), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, skillRelPath), nil
}

// writeSkill installs body at target, leaving the previous file in a timestamped
// backup when it differs. Identical content is left untouched so re-running
// onboard never churns the file.
func writeSkill(out io.Writer, target, body string) error {
	existing, err := os.ReadFile(target) //nolint:gosec // path is derived from $HOME or an explicit --project
	switch {
	case err == nil && bytes.Equal(existing, []byte(body)):
		fmt.Fprintf(out, "skill: already up-to-date at %s\n", target)
		return nil
	case err == nil:
		backup := fmt.Sprintf("%s.bak.%s", target, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.WriteFile(backup, existing, 0o600); err != nil {
			return fmt.Errorf("back up %s: %w", target, err)
		}
		fmt.Fprintf(out, "skill: backed up previous version to %s\n", backup)
	case !os.IsNotExist(err):
		return fmt.Errorf("read %s: %w", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}
	// Write via a temp file in the same directory so a failure never leaves a
	// half-written skill behind (spec § 4.5).
	tmp, err := os.CreateTemp(filepath.Dir(target), ".0ops-skill-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", filepath.Dir(target), err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("install %s: %w", target, err)
	}

	fmt.Fprintf(out, "skill: installed %s\n", target)
	return nil
}
