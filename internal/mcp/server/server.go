// Package server provides the 0ops MCP server implementation.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winshare/zeroops/internal/shared"
	"github.com/winshare/zeroops/internal/shared/authconfig"
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

type getAppInput struct {
	TeamSlug string `json:"team_slug"`
	AppSlug  string `json:"app_slug"`
}

type listTeamsInput struct{}

func registerTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_teams",
		Description: "List teams available to the current actor.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listTeamsInput) (*mcp.CallToolResult, dto.ListTeamsResponse, error) {
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.ListTeamsResponse{}, err
		}

		out, err := backendclient.New(host, token).ListTeams(ctx)
		if err != nil {
			return nil, dto.ListTeamsResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_apps",
		Description: "List apps in a team.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listAppsInput) (*mcp.CallToolResult, dto.ListAppsResponse, error) {
		if input.TeamSlug == "" {
			return nil, dto.ListAppsResponse{}, fmt.Errorf("team_slug is required")
		}

		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.ListAppsResponse{}, err
		}

		client := backendclient.New(host, token)
		out, err := client.ListApps(ctx, input.TeamSlug, input.PageSize, input.Cursor)
		if err != nil {
			return nil, dto.ListAppsResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_app",
		Description: "Get an app in a team.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getAppInput) (*mcp.CallToolResult, dto.AppRef, error) {
		if input.TeamSlug == "" {
			return nil, dto.AppRef{}, fmt.Errorf("team_slug is required")
		}
		if input.AppSlug == "" {
			return nil, dto.AppRef{}, fmt.Errorf("app_slug is required")
		}

		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.AppRef{}, err
		}

		out, err := backendclient.New(host, token).GetApp(ctx, input.TeamSlug, input.AppSlug)
		if err != nil {
			return nil, dto.AppRef{}, err
		}
		return nil, out, nil
	})
}

func resolveBackendAuth() (string, string, error) {
	cfg, _ := authconfig.Load()

	host := ""
	if token, ok := cfg.First(); ok {
		host = token.Host
	}
	if host == "" {
		host = "http://127.0.0.1:8080"
	}

	if envHost := os.Getenv("OPS_HOST"); envHost != "" {
		host = envHost
	}

	token := os.Getenv("OPS_BEARER_TOKEN")
	if token == "" {
		if fromFile, ok := cfg.BearerForHost(host); ok {
			token = fromFile
		}
	}
	if strings.TrimSpace(token) == "" {
		return "", "", fmt.Errorf("unauthorized: please run 0ops auth login")
	}

	return host, token, nil
}
