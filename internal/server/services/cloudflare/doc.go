// Package cloudflare provides Cloudflare API client and tunnel management utilities.
//
// # Overview
//
// The cloudflare package manages:
//   - DNS records for app subdomains (CNAME routing to tunnel)
//   - Cloudflare Tunnel configuration and route updates
//   - SSL/TLS certificate management (via Cloudflare's universal wildcard cert)
//
// # Integration Points
//
//   - Server bootstrap: Loads TUNNEL_ID, API_TOKEN from environment
//   - CreateApp flow: Creates DNS CNAME records via RouteAppToDomain
//   - DeleteApp flow: Cleans up DNS records (deferred to M3+)
//   - Deploy flow: Updates tunnel routes after deploy (deferred to M3+)
//
// # M2 Implementation Strategy
//
// M2 introduces the following no-op stubs:
//   - RouteAppToDomain: DNS CNAME record creation (skeleton)
//   - CreateTunnelRoute: Tunnel config update (skeleton)
//   - DeleteTunnelRoute: Tunnel route deletion (skeleton)
//   - GetDomainStatus: Domain verification (skeleton)
//
// All methods return nil when DisableTunnelIsolation=true (for dev/test).
// Full implementation (Cloudflare Go SDK, API calls, retries) deferred to M3+.
//
// # M3+ Implementation Plan
//
// - Add github.com/cloudflare/cloudflare-go dependency
// - Load TUNNEL_ID from CF_TUNNEL_ID env var
// - Load API token from CF_API_TOKEN env var
// - Implement REST API calls:
//   - POST /accounts/{account_id}/cfd_tunnel/{tunnel_id}/routes
//   - DELETE /accounts/{account_id}/cfd_tunnel/{tunnel_id}/routes/{route_id}
//   - PATCH /accounts/{account_id}/dns_records/{zone_id}/{record_id}
//
// - Add circuit breaker + exponential backoff for API transients
// - Add integration test with Cloudflare sandbox (if available)
//
// # Configuration
//
// Example setup in cmd/server/main.go:
//
//	cf := &cloudflare.Config{
//	    TunnelID:                  os.Getenv("CF_TUNNEL_ID"),
//	    APIToken:                  os.Getenv("CF_API_TOKEN"),
//	    AccountID:                 os.Getenv("CF_ACCOUNT_ID"),
//	    ZoneID:                    os.Getenv("CF_ZONE_ID"),
//	    DisableTunnelIsolation:    os.Getenv("CF_DISABLE_TUNNEL") == "true",
//	}
//	cfClient, err := cloudflare.NewClient(cf)
//	if err != nil {
//	    panic(err)
//	}
//
// # Error Handling
//
// Cloudflare API errors should be categorized:
//   - Rate limit (HTTP 429): Retry with exponential backoff
//   - Invalid token (HTTP 401): Fatal, log security event
//   - Zone/tunnel not found (HTTP 404): Fatal, config validation failure
//   - Transient (5xx): Retry with backoff
//
// # References
//
// - docs/features/cloudflare-tunnel/spec.md (domain structure, route patterns)
// - internal/server/services/k3s/doc.go (similar skeletal approach)
package cloudflare
