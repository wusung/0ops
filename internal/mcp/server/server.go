package server

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winshare/zeroops/internal/shared"
)

func Implementation() *mcp.Implementation {
	return &mcp.Implementation{
		Name:    "0ops-mcp",
		Version: shared.Version,
	}
}

func New(logger *slog.Logger) *mcp.Server {
	return mcp.NewServer(Implementation(), &mcp.ServerOptions{
		Logger: logger,
	})
}
