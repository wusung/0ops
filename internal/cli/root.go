// Package cli provides the root command for the 0ops CLI.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/winshare/zeroops/internal/shared"
)

// NewRootCommand returns the root 0ops CLI command.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "0ops",
		Short:         "0ops CLI — internal PaaS control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       shared.Version,
	}
	root.SetVersionTemplate("0ops {{.Version}}\n")
	return root
}
