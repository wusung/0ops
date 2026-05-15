package domainverify

// Status is the domain_binding lifecycle state. Source: spec § 4 + § 8.3.
type Status string

const (
	// StatusPending marks a domain_binding row that is not yet DNS-verified.
	StatusPending Status = "pending"
	// StatusVerified marks a fully verified, healthy custom hostname.
	StatusVerified Status = "verified"
	// StatusUnhealthy marks a previously verified hostname that has been
	// failing health checks; still serving inside the 7-day grace window.
	StatusUnhealthy Status = "unhealthy"
	// StatusExpired marks a pending hostname that ran past its 24h+extend TTL.
	StatusExpired Status = "expired"
	// StatusReleased marks an hostname that has been unbinded (Cloudflare
	// hostname deleted, ingress hostname removed).
	StatusReleased Status = "released"
)

// Known reports whether s is one of the canonical statuses.
func (s Status) Known() bool {
	switch s {
	case StatusPending, StatusVerified, StatusUnhealthy, StatusExpired, StatusReleased:
		return true
	}
	return false
}

// CanTransition reports whether the transition from -> to is permitted by the
// spec § 4 state machine.
func CanTransition(from, to Status) bool {
	if !from.Known() || !to.Known() {
		return false
	}
	switch from {
	case StatusPending:
		return to == StatusVerified || to == StatusExpired || to == StatusReleased
	case StatusVerified:
		return to == StatusUnhealthy || to == StatusReleased
	case StatusUnhealthy:
		return to == StatusVerified || to == StatusReleased
	}
	return false
}
