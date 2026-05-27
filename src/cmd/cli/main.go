// Package main provides the 0ops CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/winshare/zeroops/internal/cli"
)

func main() {
	root := cli.NewRootCommand()

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
