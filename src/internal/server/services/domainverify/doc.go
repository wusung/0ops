// Package domainverify implements customer-owned domain add/verify/extend/remove.
//
// This package owns the policy and state machine for `domain_binding` rows of
// `kind=extra`: apex detection, hostname validation, verification token
// generation, dual-condition DNS verification (CNAME/A + TXT), 24h TTL with
// extend × 2, and 7-day grace before unbinding unhealthy hostnames.
//
// Source spec: docs/features/custom-domain-and-verify/spec.md.
// Architecture decision: docs/adrs/0007-customer-domain-tls.md.
//
// # Scope (M3.1)
//
// Only the policy / state-machine core lives here. The following M3-wrap-up
// items are intentionally deferred:
//
//   - schema migration that adds `is_apex`, `extends_used`,
//     `health_check_failed_at`, `status` to the `domain_binding` table;
//   - HTTP routes (`POST .../domains:preview`, `POST .../domains`,
//     `POST .../domains/{host}:verify`, `POST .../domains/{host}:extend`,
//     `DELETE .../domains/{host}`);
//   - Cloudflare Custom Hostname API client (`POST /zones/{zid}/custom_hostnames`
//     and friends) — currently `internal/server/services/cloudflare/` only
//     implements wildcard tunnel route ops;
//   - 0ops-gitops ingress.yaml render for verified hostnames;
//   - audit_log adapter implementation;
//   - CLI `0ops domains verify ... --extend`;
//   - MCP `add_domain_preview` / `verify_domain` tools.
//
// All side-effect surfaces above are exposed as interfaces (Store, Resolver,
// CloudflareHostnameAPI, Auditor, LeaderProbe) so the wiring task can plug
// concrete implementations without changing this package.
package domainverify
