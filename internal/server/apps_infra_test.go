package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/cloudflare"
	"github.com/winshare/zeroops/internal/server/services/k3s"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// mockK3sClient implements infraK3sClient for testing.
type mockK3sClient struct {
	ensureNamespaceCalled bool
	ensureQuotaCalled     bool
	ensureLimitCalled     bool
	ensurePolicyCalled    bool
	patchPSACalled        bool
}

func (m *mockK3sClient) EnsureNamespace(_ context.Context, _, _, _ string) (string, error) {
	m.ensureNamespaceCalled = true
	return "team-test", nil
}

func (m *mockK3sClient) EnsureResourceQuota(_ context.Context, _, _ string) error {
	m.ensureQuotaCalled = true
	return nil
}

func (m *mockK3sClient) EnsureLimitRange(_ context.Context, _ string) error {
	m.ensureLimitCalled = true
	return nil
}

func (m *mockK3sClient) EnsureNetworkPolicy(_ context.Context, _ string) error {
	m.ensurePolicyCalled = true
	return nil
}

func (m *mockK3sClient) PatchNamespacePSA(_ context.Context, _ string) error {
	m.patchPSACalled = true
	return nil
}

// mockCloudflareClient implements infraCloudflareClient for testing.
type mockCloudflareClient struct {
	routeAppToDomainCalled bool
	createTunnelRouteCalled bool
}

func (m *mockCloudflareClient) RouteAppToDomain(_ context.Context, _, _, _ string) (string, error) {
	m.routeAppToDomainCalled = true
	return "test.winshare.tw", nil
}

func (m *mockCloudflareClient) CreateTunnelRoute(_ context.Context, _, _, _ string) error {
	m.createTunnelRouteCalled = true
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

func TestCreateAppConfirmCallsK8sIsolationOperations(t *testing.T) {
	store, token := newFakeStore()
	k3sClient := &mockK3sClient{}
	router := NewRouterWithInfra(store, k3sClient, nil)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewCreateApp(context.Background(), store.team.Slug, dto.AppCreateRequest{
		Slug:    "infra-nextdemo",
		RepoURL: "https://github.com/example/nextdemo",
		Ref:     "main",
	})
	if err != nil {
		t.Fatalf("PreviewCreateApp() error = %v", err)
	}
	if _, err := client.CreateApp(context.Background(), store.team.Slug, dto.ConfirmCreateAppRequest{PreviewID: preview.PreviewID}); err != nil {
		t.Fatalf("CreateApp() error = %v", err)
	}

	if !k3sClient.ensureNamespaceCalled {
		t.Fatalf("EnsureNamespace() not called")
	}
	if !k3sClient.ensureQuotaCalled {
		t.Fatalf("EnsureResourceQuota() not called")
	}
	if !k3sClient.ensureLimitCalled {
		t.Fatalf("EnsureLimitRange() not called")
	}
	if !k3sClient.ensurePolicyCalled {
		t.Fatalf("EnsureNetworkPolicy() not called")
	}
	if !k3sClient.patchPSACalled {
		t.Fatalf("PatchNamespacePSA() not called")
	}
}

func TestCreateAppConfirmCallsCloudflareBindingOperations(t *testing.T) {
	store, token := newFakeStore()
	cfClient := &mockCloudflareClient{}
	router := NewRouterWithInfra(store, nil, cfClient)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewCreateApp(context.Background(), store.team.Slug, dto.AppCreateRequest{
		Slug:    "cf-nextdemo",
		RepoURL: "https://github.com/example/nextdemo",
		Ref:     "main",
	})
	if err != nil {
		t.Fatalf("PreviewCreateApp() error = %v", err)
	}
	out, err := client.CreateApp(context.Background(), store.team.Slug, dto.ConfirmCreateAppRequest{PreviewID: preview.PreviewID})
	if err != nil {
		t.Fatalf("CreateApp() error = %v", err)
	}

	if !cfClient.routeAppToDomainCalled {
		t.Fatalf("RouteAppToDomain() not called")
	}
	if !cfClient.createTunnelRouteCalled {
		t.Fatalf("CreateTunnelRoute() not called")
	}
	if out.SubdomainURL != "https://test.winshare.tw" {
		t.Fatalf("SubdomainURL = %q, want https://test.winshare.tw", out.SubdomainURL)
	}
}
