// Package cli — onboard subcommand.
// spec: docs/features/end-user-onboarding/spec.md
//
// 0ops onboard <host> 把 install 後的登入串成一條指令：
//
//	確認 auth.json 對 <host> 已有有效 token；無則跑 device-flow login
//	把 skill 裝到使用者層，讓 agent 認得 0ops（--skip-skill 可略過）
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wusung/0ops/internal/cli/skillasset"
	"github.com/wusung/0ops/internal/shared/authconfig"
)

func newOnboardCommand() *cobra.Command {
	var skipLogin bool
	var skipSkill bool
	cmd := &cobra.Command{
		Use:   "onboard <host>",
		Short: "One-shot setup: login to this 0ops backend",
		Long: `Glue step run by scripts/install.sh and re-runnable by hand:

  - Checks auth.json for an existing token for <host>.
  - If missing, runs the device-flow login against <host>.
  - Installs the 0ops skill at the user level so your AI CLI knows these
    commands (skip with --skip-skill; re-runnable as: 0ops skill install).

Idempotent: re-run is a no-op when login is fresh and the skill is current.`,
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

			// --- 2. skill ---
			// A failed install must not fail onboard: the login already
			// succeeded and the skill can be added afterwards.
			if !skipSkill {
				target, err := skillTargetPath(cmd, "")
				if err == nil {
					err = writeSkill(out, target, skillasset.Markdown())
				}
				if err != nil {
					fmt.Fprintf(out, "skill: install failed (%v)\n", err)
					fmt.Fprintf(out, "skill: run `0ops skill install` to retry\n")
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&skipLogin, "skip-login", false, "skip device-flow login step")
	cmd.Flags().BoolVar(&skipSkill, "skip-skill", false, "skip installing the 0ops skill")
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

func normalizeOpsHost(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimRight(v, "/")
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		v = "https://" + v
	}
	return v
}
