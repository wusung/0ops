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

type inspectRepoInput struct {
	TeamSlug string `json:"team_slug"`
	AppSlug  string `json:"app_slug"`
}

type deployStatusInput struct {
	TeamSlug string `json:"team_slug"`
	AppSlug  string `json:"app_slug"`
}

type tailLogsInput struct {
	TeamSlug string `json:"team_slug"`
	AppSlug  string `json:"app_slug"`
	Limit    int    `json:"limit,omitempty"`
}

type listDomainsInput struct {
	TeamSlug string `json:"team_slug"`
	AppSlug  string `json:"app_slug"`
}

type listMembersInput struct {
	TeamSlug string `json:"team_slug"`
}

type inviteMemberPreviewInput struct {
	TeamSlug    string  `json:"team_slug"`
	Role        string  `json:"role"`
	GithubLogin *string `json:"github_login,omitempty"`
	Email       *string `json:"email,omitempty"`
}

type inviteMemberInput struct {
	TeamSlug  string `json:"team_slug"`
	PreviewID string `json:"preview_id"`
}

type removeMemberPreviewInput struct {
	TeamSlug string `json:"team_slug"`
	UserID   string `json:"user_id"`
}

type removeMemberInput struct {
	TeamSlug  string `json:"team_slug"`
	PreviewID string `json:"preview_id"`
}

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
		out, err := backendclient.New(host, token).ListApps(ctx, input.TeamSlug, input.PageSize, input.Cursor)
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

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "inspect_repo",
		Description: "Inspect app repository metadata.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input inspectRepoInput) (*mcp.CallToolResult, dto.RepoInspectResponse, error) {
		if input.TeamSlug == "" || input.AppSlug == "" {
			return nil, dto.RepoInspectResponse{}, fmt.Errorf("team_slug and app_slug are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.RepoInspectResponse{}, err
		}
		out, err := backendclient.New(host, token).InspectRepo(ctx, input.TeamSlug, input.AppSlug)
		if err != nil {
			return nil, dto.RepoInspectResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_deploy_status",
		Description: "Get latest deploy status for an app.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input deployStatusInput) (*mcp.CallToolResult, dto.DeployStatusResponse, error) {
		if input.TeamSlug == "" || input.AppSlug == "" {
			return nil, dto.DeployStatusResponse{}, fmt.Errorf("team_slug and app_slug are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.DeployStatusResponse{}, err
		}
		out, err := backendclient.New(host, token).GetDeployStatus(ctx, input.TeamSlug, input.AppSlug)
		if err != nil {
			return nil, dto.DeployStatusResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tail_logs",
		Description: "Tail latest deploy logs for an app.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input tailLogsInput) (*mcp.CallToolResult, dto.TailLogsResponse, error) {
		if input.TeamSlug == "" || input.AppSlug == "" {
			return nil, dto.TailLogsResponse{}, fmt.Errorf("team_slug and app_slug are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.TailLogsResponse{}, err
		}
		out, err := backendclient.New(host, token).TailLogs(ctx, input.TeamSlug, input.AppSlug, input.Limit)
		if err != nil {
			return nil, dto.TailLogsResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_domains",
		Description: "List domains for an app.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listDomainsInput) (*mcp.CallToolResult, dto.ListDomainsResponse, error) {
		if input.TeamSlug == "" || input.AppSlug == "" {
			return nil, dto.ListDomainsResponse{}, fmt.Errorf("team_slug and app_slug are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.ListDomainsResponse{}, err
		}
		out, err := backendclient.New(host, token).ListDomains(ctx, input.TeamSlug, input.AppSlug)
		if err != nil {
			return nil, dto.ListDomainsResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_members",
		Description: "List members in a team.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listMembersInput) (*mcp.CallToolResult, dto.ListMembersResponse, error) {
		if input.TeamSlug == "" {
			return nil, dto.ListMembersResponse{}, fmt.Errorf("team_slug is required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.ListMembersResponse{}, err
		}
		out, err := backendclient.New(host, token).ListMembers(ctx, input.TeamSlug)
		if err != nil {
			return nil, dto.ListMembersResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "invite_member_preview",
		Description: "Create preview for inviting a team member.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input inviteMemberPreviewInput) (*mcp.CallToolResult, dto.PreviewResponse, error) {
		if input.TeamSlug == "" {
			return nil, dto.PreviewResponse{}, fmt.Errorf("team_slug is required")
		}
		if input.Role == "" {
			return nil, dto.PreviewResponse{}, fmt.Errorf("role is required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.PreviewResponse{}, err
		}
		out, err := backendclient.New(host, token).PreviewInviteMember(ctx, input.TeamSlug, dto.InviteMemberRequest{
			Role:        input.Role,
			GithubLogin: input.GithubLogin,
			Email:       input.Email,
		})
		if err != nil {
			return nil, dto.PreviewResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "invite_member",
		Description: "Confirm member invite using preview_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input inviteMemberInput) (*mcp.CallToolResult, dto.InviteMemberResponse, error) {
		if input.TeamSlug == "" || input.PreviewID == "" {
			return nil, dto.InviteMemberResponse{}, fmt.Errorf("team_slug and preview_id are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.InviteMemberResponse{}, err
		}
		out, err := backendclient.New(host, token).InviteMember(ctx, input.TeamSlug, dto.ConfirmInviteMemberRequest{
			PreviewID: input.PreviewID,
		})
		if err != nil {
			return nil, dto.InviteMemberResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remove_member_preview",
		Description: "Create preview for removing a member.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input removeMemberPreviewInput) (*mcp.CallToolResult, dto.PreviewResponse, error) {
		if input.TeamSlug == "" || input.UserID == "" {
			return nil, dto.PreviewResponse{}, fmt.Errorf("team_slug and user_id are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, dto.PreviewResponse{}, err
		}
		out, err := backendclient.New(host, token).PreviewRemoveMember(ctx, input.TeamSlug, dto.RemoveMemberRequest{
			UserID: input.UserID,
		})
		if err != nil {
			return nil, dto.PreviewResponse{}, err
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "remove_member",
		Description: "Confirm member removal using preview_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input removeMemberInput) (*mcp.CallToolResult, map[string]string, error) {
		if input.TeamSlug == "" || input.PreviewID == "" {
			return nil, nil, fmt.Errorf("team_slug and preview_id are required")
		}
		host, token, err := resolveBackendAuth()
		if err != nil {
			return nil, nil, err
		}
		if err := backendclient.New(host, token).RemoveMember(ctx, input.TeamSlug, dto.ConfirmRemoveMemberRequest{
			PreviewID: input.PreviewID,
		}); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"status": "removed"}, nil
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
