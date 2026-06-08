// Package cli — onboard subcommand.
// spec: docs/features/end-user-onboarding/spec.md
//
// 0ops onboard <host> 把 install 後到能在 AI CLI 內呼叫的整段串成一條指令：
//   1. 確認 auth.json 對 <host> 已有有效 token；無則跑 device-flow login
//   2. 偵測 host 端已裝的 AI CLI（claude / codex），對每個跑 mcp setup
//   3. 印 restart-your-ai-cli 指引
package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared/authconfig"
)

func newOnboardCommand() *cobra.Command {
	var (
		skipLogin   bool
		skipMcp     bool
		mcpHostsCSV string
		mcpBinary   string
		assumeYes   bool
	)
	cmd := &cobra.Command{
		Use:   "onboard <host>",
		Short: "One-shot setup: login + wire AI CLIs to this 0ops backend",
		Long: `Glue step run by scripts/install.sh and re-runnable by hand:

  - Checks auth.json for an existing token for <host>.
  - If missing, runs the device-flow login against <host>.
  - Detects installed AI CLI hosts (claude, codex) and writes a MCP entry
    pointing at the local 0ops-mcp binary.
  - Reports next step (restart the AI CLI).

Idempotent: re-run is a no-op when login is fresh and configs already match.

Auto-detection rules:
  - claude  → 'claude' on PATH OR ~/.claude.json exists
  - codex   → 'codex' on PATH OR ~/.codex/config.toml exists

Use --hosts to force a specific subset (comma-separated). Use --skip-mcp
to install/login only; --skip-login to wire AI CLIs only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := normalizeOpsHost(args[0])
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "onboarding 0ops at %s\n", host)

			// --- 1. login ---
			if !skipLogin {
				if alreadyLoggedIn(host) {
					fmt.Fprintf(out, "auth: already logged in to %s — skip\n", host)
				} else {
					fmt.Fprintf(out, "auth: starting device-flow login to %s\n", host)
					if err := runAuthLogin(cmd, host, "", ""); err != nil {
						return fmt.Errorf("auth login: %w", err)
					}
				}
			}

			// --- 2. mcp setup for detected (or forced) hosts ---
			if !skipMcp {
				targets := selectMcpHosts(mcpHostsCSV)
				if len(targets) == 0 {
					fmt.Fprintln(out, "mcp: no AI CLI host detected (claude / codex) — skip")
					fmt.Fprintln(out, "     install claude code or codex CLI, then run: 0ops onboard "+host)
				}
				for _, h := range targets {
					fmt.Fprintf(out, "mcp: setting up %s\n", h)
					plan, err := planMcpSetup(string(h), host, mcpBinary, "")
					if err != nil {
						return fmt.Errorf("plan %s: %w", h, err)
					}
					if err := plan.apply(cmd, assumeYes); err != nil {
						return fmt.Errorf("apply %s: %w", h, err)
					}
				}
				if len(targets) > 0 {
					fmt.Fprintln(out, "")
					fmt.Fprintln(out, "DONE — restart your AI CLI to load the 0ops MCP server.")
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&skipLogin, "skip-login", false, "skip device-flow login step")
	cmd.Flags().BoolVar(&skipMcp, "skip-mcp", false, "skip AI CLI MCP setup step")
	cmd.Flags().StringVar(&mcpHostsCSV, "hosts", "", "force specific AI CLI hosts (csv: claude-code,codex)")
	cmd.Flags().StringVar(&mcpBinary, "mcp-binary", "", "path to 0ops-mcp binary (default: auto)")
	cmd.Flags().BoolVar(&assumeYes, "yes", true, "skip overwrite prompts for existing MCP entries")
	return cmd
}

// alreadyLoggedIn returns true when auth.json has a non-empty bearer token for host.
func alreadyLoggedIn(host string) bool {
	cfg, err := authconfig.Load()
	if err != nil {
		return false
	}
	tok, ok := cfg.TokenForHost(host)
	if !ok {
		return false
	}
	return strings.TrimSpace(tok.BearerToken) != ""
}

// selectMcpHosts returns the list of AI CLI hosts to wire up.
// If csv is non-empty, parses + validates. Otherwise auto-detects.
func selectMcpHosts(csv string) []mcpHost {
	if csv != "" {
		var out []mcpHost
		for _, raw := range strings.Split(csv, ",") {
			h, err := resolveMcpHostKey(strings.TrimSpace(raw))
			if err == nil {
				out = append(out, h)
			}
		}
		return out
	}
	var out []mcpHost
	if detectClaudeCode() {
		out = append(out, hostClaudeCode)
	}
	if detectCodex() {
		out = append(out, hostCodex)
	}
	return out
}

func detectClaudeCode() bool {
	if _, err := exec.LookPath("claude"); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, p := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".config", "claude-code", "config.json"),
	} {
		if fileExists(p) {
			return true
		}
	}
	return false
}

func detectCodex() bool {
	if _, err := exec.LookPath("codex") ; err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return fileExists(filepath.Join(home, ".codex", "config.toml")) ||
		fileExists(filepath.Join(home, ".codex"))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
