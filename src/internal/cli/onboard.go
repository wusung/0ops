// Package cli — onboard subcommand.
// spec: docs/features/end-user-onboarding/spec.md
//
// 0ops onboard <host> 把 install 後的登入串成一條指令：
//
//	確認 auth.json 對 <host> 已有有效 token；無則跑 device-flow login
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/winshare/zeroops/internal/shared/authconfig"
)

func newOnboardCommand() *cobra.Command {
	var skipLogin bool
	cmd := &cobra.Command{
		Use:   "onboard <host>",
		Short: "One-shot setup: login to this 0ops backend",
		Long: `Glue step run by scripts/install.sh and re-runnable by hand:

  - Checks auth.json for an existing token for <host>.
  - If missing, runs the device-flow login against <host>.

Idempotent: re-run is a no-op when login is fresh.`,
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

			return nil
		},
	}
	cmd.Flags().BoolVar(&skipLogin, "skip-login", false, "skip device-flow login step")
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
