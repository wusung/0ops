package sso

import "time"

// SSOTokenMaxTTL is the hard ceiling for SSO-issued bearer tokens (spec § 7.3,
// hard rule #6). SSO tokens never roll, so this bounds the worst-case window
// between an IdP-side disable and 0ops token expiry.
const SSOTokenMaxTTL = 24 * time.Hour

// SSOTokenExpiry computes an SSO bearer token's absolute expiry as
// min(IdP session, session_max_ttl_s, 24h) measured from now (spec § 7.3).
// A non-positive or oversized session_max_ttl_s falls back to the 24h ceiling;
// a zero idpExpiry means the IdP did not bound the session.
func SSOTokenExpiry(now, idpExpiry time.Time, sessionMaxTTLSeconds int) time.Time {
	ttl := time.Duration(sessionMaxTTLSeconds) * time.Second
	if ttl <= 0 || ttl > SSOTokenMaxTTL {
		ttl = SSOTokenMaxTTL
	}
	exp := now.Add(ttl)
	if !idpExpiry.IsZero() && idpExpiry.Before(exp) {
		exp = idpExpiry
	}
	return exp
}
