package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "github.com/winshare/zeroops/internal/mcp/server"
	"github.com/winshare/zeroops/internal/shared"
)

func main() {
	// MCP stdio: never log to stdout. Logs go to stderr.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := mcpserver.New(logger)

	logger.Info("0ops-mcp starting", "version", shared.Version)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		logger.Error("mcp server exited", "err", err)
		os.Exit(1)
	}
}
