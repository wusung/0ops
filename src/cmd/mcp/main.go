// Package main provides the 0ops MCP entrypoint.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winshare/zeroops/internal/mcp/lint"
	mcpserver "github.com/winshare/zeroops/internal/mcp/server"
	"github.com/winshare/zeroops/internal/shared"
)

// exitCodeLintFailed signals that the startup tool-description lint contract
// rejected one or more tools (spec § 4.6). Exit 2 is reserved for "this is
// not a runtime error, this is a configuration error".
const exitCodeLintFailed = 2

func main() {
	os.Exit(run(context.Background(), os.Stderr))
}

func run(ctx context.Context, stderr io.Writer) int {
	// MCP stdio: never log to stdout. Logs go to stderr.
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, registry := mcpserver.NewWithRegistry(logger)

	if code := reportLintViolations(lint.ApplyAll(registry.Tools()), stderr); code != 0 {
		return code
	}

	logger.Info("0ops-mcp starting", "version", shared.Version)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("mcp server exited", "err", err)
		return 1
	}
	return 0
}

// reportLintViolations writes each lint violation to stderr and returns
// exitCodeLintFailed when any violation is present. No violations -> 0.
func reportLintViolations(errs []error, stderr io.Writer) int {
	if len(errs) == 0 {
		return 0
	}
	for _, err := range errs {
		fmt.Fprintln(stderr, err)
	}
	fmt.Fprintf(stderr, "[mcp-lint] %d violation(s). Refer to docs/0ops-plan.md \"MCP server / Tool description 強制約定\" section. Aborting startup.\n", len(errs))
	return exitCodeLintFailed
}
