// Package server provides the 0ops MCP server implementation.
package server

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winshare/zeroops/internal/shared"
)

// Implementation returns the MCP server metadata.
func Implementation() *mcp.Implementation {
	return &mcp.Implementation{
		Name:    "0ops-mcp",
		Version: shared.Version,
	}
}

// New returns a configured MCP server.
func New(logger *slog.Logger) *mcp.Server {
	return mcp.NewServer(Implementation(), &mcp.ServerOptions{
		Logger: logger,
	})
}
