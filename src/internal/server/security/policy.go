package security

// Token TTL defaults / global ceilings (spec § 7, auth-and-rbac § 4.3).
// These mirror the shipped signing path:
//   - PAT default 90d, max 365d (src/internal/server/apps.go createTokenHandler)
//   - device flow 30d rolling (src/internal/server/db/apps.go DeviceTokenTTL)
//
// Any change here MUST be mirrored in auth-and-rbac § 4.3/§ 4.4 (spec hard
// rule #10). This package only computes the ceiling; v1 does not wire it
// into the signing path (team_security_policy schema is spec § 12 open).
const (
	// DefaultPATTTLDays is the TTL applied when a PAT request omits a duration.
	DefaultPATTTLDays = 90
	// GlobalMaxPATTTLDays is the absolute PAT ceiling; team policy may only
	// tighten below it, never raise above (spec hard rule #6).
	GlobalMaxPATTTLDays = 365
	// GlobalMaxDeviceTTLDays is the device-flow rolling-token ceiling.
	GlobalMaxDeviceTTLDays = 30
)

// TTLPolicy is a team-level security policy (spec § 7.2). A zero field means
// "unset" — the global ceiling applies. Policy can only tighten, never relax.
type TTLPolicy struct {
	// MaxPATTTLDays caps PAT lifetime for the team; 0 = unset.
	MaxPATTTLDays int
	// MaxDeviceTTLDays caps device-flow token lifetime; 0 = unset.
	MaxDeviceTTLDays int
}

// ResolvePATTTLDays returns the effective PAT TTL in days:
//
//	min(requested-or-default, team ceiling, global max)
//
// The team ceiling is itself clamped to the global max, so a policy can never
// widen the window (spec hard rule #6). Pure function.
func ResolvePATTTLDays(requested int, policy TTLPolicy) int {
	if requested <= 0 {
		requested = DefaultPATTTLDays
	}
	ceiling := GlobalMaxPATTTLDays
	if policy.MaxPATTTLDays > 0 && policy.MaxPATTTLDays < ceiling {
		ceiling = policy.MaxPATTTLDays
	}
	return min(requested, ceiling)
}

// ResolveDeviceTTLDays returns the effective device-flow TTL in days:
//
//	min(requested-or-global-default, team ceiling, global max)
func ResolveDeviceTTLDays(requested int, policy TTLPolicy) int {
	if requested <= 0 {
		requested = GlobalMaxDeviceTTLDays
	}
	ceiling := GlobalMaxDeviceTTLDays
	if policy.MaxDeviceTTLDays > 0 && policy.MaxDeviceTTLDays < ceiling {
		ceiling = policy.MaxDeviceTTLDays
	}
	return min(requested, ceiling)
}
