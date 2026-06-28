// Package security holds the pure decision and policy helpers for the
// security-hardening feature (docs/features/security-hardening/spec.md):
//
//   - risk.go    high-risk action catalog + typed-confirmation phrase (§ 5)
//   - anomaly.go token anomaly signal evaluation + reaction policy (§ 6)
//   - policy.go  token TTL ceiling resolution (§ 7)
//
// Everything here is a pure function or constant: no DB writes, no
// background loops. Audit writes still flow through services/audit; anomaly
// detection still belongs to the rate-limit-and-abuse background detector
// (deferred). This package only supplies the semantics those callers apply,
// strictly adding to — never relaxing — the existing preview/confirm gate.
package security
