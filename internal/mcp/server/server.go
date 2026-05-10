// Package server provides the 0ops MCP server implementation.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winshare/zeroops/internal/shared"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
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
	srv := mcp.NewServer(Implementation(), &mcp.ServerOptions{
		Logger: logger,
	})
	registerTools(srv)
	return srv
}

type listAppsInput struct {
	TeamSlug string `json:"team_slug"`
	PageSize int    `json:"page_size,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

func registerTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_apps",
		Description: "List apps in a team.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listAppsInput) (*mcp.CallToolResult, dto.ListAppsResponse, error) {
		if input.TeamSlug == "" {
			return nil, dto.ListAppsResponse{}, fmt.Errorf("team_slug is required")
		}

		client := backendclient.New(envOr("OPS_HOST", "http://127.0.0.1:8080"), os.Getenv("OPS_BEARER_TOKEN"))
		out, err := client.ListApps(ctx, input.TeamSlug, input.PageSize, input.Cursor)
		if err != nil {
			return nil, dto.ListAppsResponse{}, err
		}
		return nil, out, nil
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
