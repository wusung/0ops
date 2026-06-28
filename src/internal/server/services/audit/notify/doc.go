// Package notify delivers important audit_log events to per-team outbound
// webhooks via a transactional outbox (audit-event-notification spec).
//
// The unique event source is audit_log: enqueue rides the audit insert
// transaction (db.Repository.InsertAuditLog calls the AuditEnqueuer after the
// row INSERT, before COMMIT), so an audit row that matches a subscription
// always has a matching webhook_delivery row (spec § 7.1, hard rule #3). A
// background dispatcher (leader-gated) then POSTs each delivery with an
// HMAC-SHA256 signature, retries with exponential backoff, and trips a circuit
// breaker on sustained failure (spec § 7.2-7.4).
//
// Files:
//
//	catalog.go     — audit action → notify event key + metadata summariser (pure)
//	payload.go     — redacted, whitelist-only outbound payload (pure)
//	sign.go        — HMAC-SHA256(secret, timestamp + "." + body) + headers (pure)
//	ssrf.go        — https-only + private/loopback/metadata rejection (pure)
//	backoff.go     — exponential retry ladder + jitter (pure)
//	secret.go      — signing-key generation + SecretStore seam (at-rest deferred)
//	enqueue.go     — AuditEnqueuer: tx-bound catalog match + delivery INSERT
//	dispatcher.go  — background delivery worker (SKIP LOCKED poll + retry + breaker)
//	actions.go     — audit actions notify itself writes (config / breaker / redeliver)
//	metrics.go     — observer seam
package notify
