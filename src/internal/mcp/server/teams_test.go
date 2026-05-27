package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	serverpkg "github.com/winshare/zeroops/internal/server"
)

func TestListTeamsToolRoundTrip(t *testing.T) {
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

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_teams"})
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
	items, ok := decoded["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected items payload: %#v", decoded["items"])
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	cancel()
	<-errCh
}

func TestToolsListContainsM1ReadTools(t *testing.T) {
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

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}

	required := []string{"list_teams", "list_apps", "get_app", "inspect_repo", "get_deploy_status", "tail_logs", "list_domains", "create_app_preview", "create_app"}
	for _, name := range required {
		if !seen[name] {
			t.Fatalf("missing tool %q in tools/list", name)
		}
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	cancel()
	<-errCh
}
