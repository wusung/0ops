package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared"
)

func TestImplementationUsesSharedVersion(t *testing.T) {
	impl := Implementation()

	if impl.Name != "0ops-mcp" {
		t.Fatalf("Name = %q, want %q", impl.Name, "0ops-mcp")
	}
	if impl.Version != shared.Version {
		t.Fatalf("Version = %q, want %q", impl.Version, shared.Version)
	}
}

func TestNewReturnsServer(t *testing.T) {
	logger := slog.Default()
	if srv := New(logger); srv == nil {
		t.Fatal("New() returned nil")
	}
}

func TestListAppsToolRoundTrip(t *testing.T) {
	store, token := newMCPFakeStore()
	backend := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(backend.Close)

	t.Setenv("OPS_HOST", backend.URL)
	t.Setenv("OPS_BEARER_TOKEN", token)

	server := New(slog.Default())
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_apps",
		Arguments: map[string]any{"team_slug": store.team.Slug},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded["page_size"] == nil {
		t.Fatal("expected page_size in tool result")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	cancel()
	<-errCh
}

func TestListAppsToolReadsAuthConfig(t *testing.T) {
	store, token := newMCPFakeStore()
	backend := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(backend.Close)

	t.Setenv("OPS_HOST", "")
	t.Setenv("OPS_BEARER_TOKEN", "")
	writeMCPAuthFile(t, backend.URL, token)

	server := New(slog.Default())
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_apps",
		Arguments: map[string]any{"team_slug": store.team.Slug},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	cancel()
	<-errCh
}

func TestListMembersToolRoundTrip(t *testing.T) {
	store, token := newMCPFakeStore()
	backend := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(backend.Close)

	t.Setenv("OPS_HOST", backend.URL)
	t.Setenv("OPS_BEARER_TOKEN", token)

	server := New(slog.Default())
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_members",
		Arguments: map[string]any{"team_slug": store.team.Slug},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatal("expected success result")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	cancel()
	<-errCh
}

type mcpFakeStore struct {
	token      db.CliToken
	team       db.Team
	role       string
	apps       []db.App
	memberRows []db.Member
	members    bool
}

func (f mcpFakeStore) FindCliTokenByHash(ctx context.Context, tokenHash string) (db.CliToken, error) {
	if tokenHash != f.token.TokenHash {
		return db.CliToken{}, os.ErrNotExist
	}
	return f.token, nil
}

func (f mcpFakeStore) ResolveTeamBySlug(ctx context.Context, slug string) (db.Team, error) {
	if slug != f.team.Slug {
		return db.Team{}, os.ErrNotExist
	}
	return f.team, nil
}

func (f mcpFakeStore) CheckTeamMembership(ctx context.Context, teamID string, userID string) (bool, error) {
	return f.members && teamID == f.team.ID && userID == f.token.OwnerUserID, nil
}

func (f mcpFakeStore) GetTeamMembershipRole(ctx context.Context, teamID string, userID string) (string, error) {
	if !f.members || teamID != f.team.ID || userID != f.token.OwnerUserID {
		return "", os.ErrNotExist
	}
	return f.role, nil
}

func (f mcpFakeStore) ListTeamApps(ctx context.Context, teamID string, limit int32, afterID *string) ([]db.App, error) {
	if teamID != f.team.ID {
		return nil, os.ErrNotExist
	}
	return f.apps, nil
}

func (f mcpFakeStore) HasAnyOwner(ctx context.Context) (bool, error) { return false, nil }

func (f mcpFakeStore) BootstrapOwner(ctx context.Context, params db.BootstrapOwnerParams) (string, string, error) {
	return "team-bootstrap", "user-bootstrap", nil
}

func (f mcpFakeStore) ListTeamMembers(ctx context.Context, teamID string) ([]db.Member, error) {
	if teamID != f.team.ID {
		return nil, os.ErrNotExist
	}
	return append([]db.Member(nil), f.memberRows...), nil
}

func (f mcpFakeStore) CreatePreview(ctx context.Context, teamID, actorUserID, action string, args json.RawMessage, summary string) (db.Preview, error) {
	return db.Preview{
		ID:          "preview-1",
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        args,
		ExpiresAt:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func (f mcpFakeStore) GetPreview(ctx context.Context, previewID string) (db.Preview, error) {
	return db.Preview{
		ID:          previewID,
		TeamID:      f.team.ID,
		ActorUserID: f.token.OwnerUserID,
		Action:      "invite_member",
		Args:        []byte(`{"github_login":"newbie","role":"member"}`),
		ExpiresAt:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func (f mcpFakeStore) ConsumePreview(ctx context.Context, previewID string) error { return nil }

func (f mcpFakeStore) InviteMember(ctx context.Context, params db.InviteMemberParams) (db.Member, error) {
	now := time.Now().UTC()
	return db.Member{
		UserID:      "user-new",
		GithubLogin: params.GithubLogin,
		Email:       params.Email,
		Role:        params.Role,
		InvitedAt:   &now,
		JoinedAt:    &now,
	}, nil
}

func (f mcpFakeStore) RemoveMember(ctx context.Context, teamID, actorUserID, targetUserID string) error {
	return nil
}

func newMCPFakeStore() (mcpFakeStore, string) {
	token := "dev-token"
	return mcpFakeStore{
		token: db.CliToken{
			ID:          "token-1",
			OwnerUserID: "user-1",
			TeamID:      "team-1",
			TokenHash:   auth.HashBearerToken(token),
			Scopes:      []string{"apps:read", "members:manage"},
		},
		team: db.Team{
			ID:   "team-1",
			Slug: "acme",
			Name: "Acme",
		},
		role:    "admin",
		members: true,
		apps: []db.App{
			{ID: "1", TeamID: "team-1", Slug: "alpha"},
			{ID: "2", TeamID: "team-1", Slug: "beta"},
		},
		memberRows: []db.Member{{UserID: "user-1", GithubLogin: strPtr("owner"), Role: "owner"}},
	}, token
}

func strPtr(v string) *string { return &v }

func writeMCPAuthFile(t *testing.T, host, token string) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path := filepath.Join(dir, "0ops")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	payload := map[string]any{
		"version": 1,
		"tokens": []map[string]any{
			{
				"host":         host,
				"bearer_token": token,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
