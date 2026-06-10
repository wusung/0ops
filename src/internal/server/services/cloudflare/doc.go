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
// # Runtime behavior
//
// The client validates the shared platform wildcard DNS route
// (`*.<OPS_DOMAIN_BASE>`, default `*.jesontech.com`) through Cloudflare's
// REST API and returns the public hostname used by create_app.
//
// DisableTunnelIsolation keeps the client in no-op mode for local development
// and tests.
//
// # Error handling
//
// - 401 / 403: configuration missing or invalid
// - 404 or empty wildcard record set: route missing
// - 429: retry with exponential backoff, then return ErrRateLimited
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
