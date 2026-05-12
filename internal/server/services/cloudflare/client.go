package cloudflare

import (
	"context"
	"fmt"
	"strings"
)

// Config holds Cloudflare API credentials and tunnel configuration.
type Config struct {
	// TunnelID is the Cloudflare Tunnel UUID.
	// Retrieved from CF_TUNNEL_ID env var.
	TunnelID string

	// APIToken is the Cloudflare API token with DNS + tunnel permissions.
	// Retrieved from CF_API_TOKEN env var.
	APIToken string

	// AccountID is the Cloudflare Account ID.
	// Retrieved from CF_ACCOUNT_ID env var.
	AccountID string

	// ZoneID is the Cloudflare Zone ID for the domain (e.g., winshare.tw).
	// Retrieved from CF_ZONE_ID env var.
	ZoneID string

	// DisableTunnelIsolation disables all Cloudflare operations.
	// Useful for development/testing without Cloudflare account.
	DisableTunnelIsolation bool
}

// Client wraps Cloudflare API calls for tunnel and DNS management.
type Client struct {
	config *Config
}

// NewClient creates a new Cloudflare client.
// If DisableTunnelIsolation is true, returns a no-op client.
// M2 implementation: Returns no-op client; actual API setup deferred to M3+.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	// TODO: Implement actual Cloudflare client initialization in future iteration
	// - Add github.com/cloudflare/cloudflare-go dependency
	// - Validate APIToken, TunnelID, AccountID, ZoneID are set
	// - Initialize NewWithAPIToken(ctx, cfg.APIToken)
	// - Test connectivity with GetZone(ctx, cfg.ZoneID)
	// - Verify tunnel exists with GetTunnel(ctx, cfg.TunnelID)

	return &Client{config: cfg}, nil
}

// RouteAppToDomain creates a DNS CNAME record for an app subdomain.
// Subdomain format: {app-slug}.{team-slug}.{base-domain}
// Example: demo.team1.winshare.tw → CNAME to tunnel.winshare.tw
//
// M2 implementation: No-op, returns subdomain name only.
// M3+ implementation: Create DNS CNAME via API.
func (c *Client) RouteAppToDomain(_ context.Context, _, _ string, appSlug string) (string, error) {
	appSlug = strings.TrimSpace(appSlug)
	if appSlug == "" {
		return "", fmt.Errorf("app slug is required")
	}
	if c.config.DisableTunnelIsolation {
		return fmt.Sprintf("%s.winshare.tw", appSlug), nil
	}

	subdomain := fmt.Sprintf("%s.winshare.tw", appSlug)

	// TODO: Implement in M3+:
	// 1. Construct CNAME target: tunnel.winshare.tw (or dynamic based on tunnel config)
	// 2. Call c.client.CreateDNSRecord(ctx, c.config.ZoneID, &dns.Record{
	//      Name:    subdomain,
	//      Type:    "CNAME",
	//      Content: tunnelCNAME,
	//      TTL:     1 (auto), // Cloudflare
	//      Proxied: true,     // Orange cloud
	//    })
	// 3. Return (subdomain, error)
	// 4. Handle error cases:
	//    - Zone not found (HTTP 404): validation failure
	//    - Rate limit (HTTP 429): retry with backoff
	//    - Duplicate record (HTTP 400): log warning, continue

	return subdomain, nil
}

// CreateTunnelRoute creates or updates an ingress route in Cloudflare Tunnel.
// Routes map HTTP requests to backend services (e.g., K3s app service).
//
// M2 implementation: No-op.
// M3+ implementation: Create route via tunnel API.
func (c *Client) CreateTunnelRoute(_ context.Context, _, _, _ string) error {
	if c.config.DisableTunnelIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Construct route pattern: {appSlug}.*.winshare.tw/*
	// 2. Call c.client.CreateTunnelRoute(ctx, &tunnel.CreateRouteRequest{
	//      Pattern:     pattern,
	//      Service:     backendURL, // e.g., http://localhost:8080
	//      Config:      &tunnel.RouteConfig{TTL: ...},
	//    })
	// 3. Handle transient failures with backoff
	// 4. Log warning if route already exists (idempotent on retry)

	return nil
}

// DeleteTunnelRoute removes an ingress route from Cloudflare Tunnel.
// Called during app deletion or team archival.
//
// M2 implementation: No-op.
// M3+ implementation: Delete route via tunnel API.
func (c *Client) DeleteTunnelRoute(_ context.Context, _ string) error {
	if c.config.DisableTunnelIsolation {
		return nil
	}

	// TODO: Implement in M3+:
	// 1. Look up route by pattern in tunnel config
	// 2. Call c.client.DeleteTunnelRoute(ctx, routeID)
	// 3. Handle 404 gracefully (route may already be deleted)
	// 4. Log deletion event

	return nil
}

// GetDomainStatus retrieves the status of a domain routing.
// Returns DNS record ID, CNAME target, and verification status.
//
// M2 implementation: Returns nil.
// M3+ implementation: Fetch DNS record details via API.
func (c *Client) GetDomainStatus(_ context.Context, _ string) (map[string]interface{}, error) {
	if c.config.DisableTunnelIsolation {
		return nil, nil
	}

	// TODO: Implement in M3+:
	// 1. Query DNS record by name: c.client.GetDNSRecord(ctx, c.config.ZoneID, subdomain)
	// 2. Query tunnel route by pattern
	// 3. Return composite status:
	//    {
	//      "dns_record_id": "...",
	//      "cname_target": "...",
	//      "proxied": true,
	//      "ttl": 1,
	//      "tunnel_route_id": "...",
	//      "tunnel_service": "...",
	//    }
	// 4. Handle missing records gracefully

	return nil, nil
}
