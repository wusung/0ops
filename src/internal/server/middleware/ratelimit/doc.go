// Package ratelimit enforces per-token and per-team token-bucket rate limits
// across categories (read / write / preview_create) keyed off plan tier
// (ADR-0011 § 3.1). The middleware MUST run after auth.Bearer + auth.ResolveTeam
// so that token id, team id, and plan are present in the request context.
//
// In-memory only (M4.2 scope); shared bucket via Redis is deferred to M5
// (spec § 4.4 + § 14 hard rule #6).
package ratelimit
