package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/cloudflare"
	"github.com/winshare/zeroops/internal/server/services/k3s"
)

// mockK3sClient implements infraK3sClient for testing.
type mockK3sClient struct {
	ensureTeamIsolationCalled bool
}

func (m *mockK3sClient) EnsureTeamIsolation(_ context.Context, _, _, _ string) (string, error) {
	m.ensureTeamIsolationCalled = true
	return "team-test", nil
}

func (m *mockK3sClient) EnsureResourceQuota(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockK3sClient) EnsureLimitRange(_ context.Context, _ string) error {
	return nil
}

func (m *mockK3sClient) EnsureNetworkPolicy(_ context.Context, _ string) error {
	return nil
}

func (m *mockK3sClient) PatchNamespacePSA(_ context.Context, _ string) error {
	return nil
}

// mockCloudflareClient implements infraCloudflareClient for testing.
type mockCloudflareClient struct {
	routeAppToDomainCalled bool
}

func (m *mockCloudflareClient) RouteAppToDomain(_ context.Context, _, _, _ string) (string, error) {
	m.routeAppToDomainCalled = true
	return "test.team.winshare.tw", nil
}

func (m *mockCloudflareClient) CreateTunnelRoute(_ context.Context, _, _, _ string) error {
	return nil
}

// TestNewRouterWithInfra verifies that infrastructure clients are accepted by NewRouterWithInfra.
func TestNewRouterWithInfra(t *testing.T) {
	k3sClient := &mockK3sClient{}
	cfClient := &mockCloudflareClient{}

	// Verify that NewRouterWithInfra returns a valid HTTP handler
	fakeStore := &fakeStore{
		token:    db.CliToken{ID: "token-1", OwnerUserID: "user-1", TeamID: "team-1"},
		members:  true,
		team:     db.Team{ID: "team-1", Slug: "team-slug", Plan: "free"},
		role:     "owner",
		hasOwner: true,
	}
	router := NewRouterWithInfra(fakeStore, k3sClient, cfClient)

	if router == nil {
		t.Fatal("NewRouterWithInfra returned nil router")
	}

	// Verify it's callable as an http.Handler
	srv := httptest.NewServer(router)
	defer srv.Close()

	_, err := http.Get(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("failed to query router: %v", err)
	}
}

// TestNewRouterWithNilInfraClients verifies that NewRouterWithInfra handles nil clients gracefully.
func TestNewRouterWithNilInfraClients(t *testing.T) {
	fakeStore := &fakeStore{
		token:    db.CliToken{ID: "token-1", OwnerUserID: "user-1", TeamID: "team-1"},
		members:  true,
		team:     db.Team{ID: "team-1", Slug: "team-slug", Plan: "free"},
		role:     "owner",
		hasOwner: true,
	}
	router := NewRouterWithInfra(fakeStore, nil, nil)

	if router == nil {
		t.Fatal("NewRouterWithInfra returned nil router with nil clients")
	}

	// Verify it's callable as an http.Handler
	srv := httptest.NewServer(router)
	defer srv.Close()

	_, err := http.Get(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("failed to query router: %v", err)
	}
}

// TestK3sClientInitialization verifies K3s client initialization with disable flag.
func TestK3sClientInitialization(t *testing.T) {
	cfg := &k3s.Config{
		DisableNamespaceIsolation: true,
	}

	client, err := k3s.NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create K3s client: %v", err)
	}

	if client == nil {
		t.Fatal("K3s client is nil")
	}

	// Verify client handles no-op case
	namespace, err := client.EnsureNamespace(context.Background(), "team-1", "team-slug", "free")
	if err != nil {
		t.Fatalf("EnsureNamespace failed: %v", err)
	}

	if namespace == "" {
		t.Fatal("EnsureNamespace returned empty namespace")
	}
}

// TestCloudflareClientInitialization verifies Cloudflare client initialization with disable flag.
func TestCloudflareClientInitialization(t *testing.T) {
	cfg := &cloudflare.Config{
		DisableTunnelIsolation: true,
	}

	client, err := cloudflare.NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create Cloudflare client: %v", err)
	}

	if client == nil {
		t.Fatal("Cloudflare client is nil")
	}

	// Verify client handles no-op case
	domain, err := client.RouteAppToDomain(context.Background(), "team-1", "team-slug", "app-slug")
	if err != nil {
		t.Fatalf("RouteAppToDomain failed: %v", err)
	}

	if domain == "" {
		t.Fatal("RouteAppToDomain returned empty domain")
	}
}
