package security

import (
	"fmt"
	"strings"
)

// Level ranks a write action by blast radius (spec § 5.1). The confirm gate
// uses it to decide whether the existing preview → confirm enforcement needs
// an *additional* typed-confirmation phrase. It never relaxes the base gate
// — it can only add (spec hard rule #1).
type Level string

const (
	// RiskNormal is the default: ordinary writes flow through the existing
	// preview/confirm gate with no extra phrase.
	RiskNormal Level = "normal"
	// RiskHigh marks reversible-but-disruptive actions (service outage,
	// privilege removal). Confirm requires a typed phrase.
	RiskHigh Level = "high"
	// RiskCritical marks irreversible / maximum-blast-radius actions.
	// Confirm requires a typed phrase.
	RiskCritical Level = "critical"
)

// riskRank orders levels so callers can ask "is this at least high?".
var riskRank = map[Level]int{RiskNormal: 0, RiskHigh: 1, RiskCritical: 2}

// AtLeast reports whether the level is at or above the given threshold.
func (l Level) AtLeast(threshold Level) bool {
	return riskRank[l] >= riskRank[threshold]
}

// riskCatalog is the explicit whitelist of high-risk actions (spec § 5.1).
// Membership is deliberate — never inferred from a blacklist. Adding a
// high-risk action is a single edit here, mirrored in the spec catalog and
// docs/features/security-hardening/baseline-matrix.md.
//
// Some entries are conditional on args (downgrade vs upgrade, privileged
// role vs member); RiskLevel applies those refinements below.
var riskCatalog = map[string]Level{
	"delete_app":           RiskCritical,
	"token_revoke":         RiskCritical,
	"plan_change":          RiskHigh, // only on downgrade; see RiskLevel
	"custom_domain_unbind": RiskHigh,
	"remove_member":        RiskHigh, // only owner/admin; see RiskLevel
	"uninstall_github_app": RiskHigh,
}

// RiskLevel classifies action+args. Actions absent from the catalog are
// RiskNormal. Pure function: no DB, no side effects (spec § 5.2).
func RiskLevel(action string, args map[string]any) Level {
	level, ok := riskCatalog[action]
	if !ok {
		return RiskNormal
	}
	switch action {
	case "plan_change":
		// Only a downgrade is high risk (quota contraction blocks new pods);
		// an upgrade or same-tier change is reversible/normal (spec § 5.1 note).
		if !isPlanDowngrade(args) {
			return RiskNormal
		}
	case "remove_member":
		// Removing a regular member is ordinary; removing owner/admin is high
		// (privilege removal, possible self-lockout) (spec § 5.1).
		if !isPrivilegedRole(args) {
			return RiskNormal
		}
	}
	return level
}

// RequiredPhrase returns the typed-confirmation phrase a high-risk action
// must echo back at confirm time, or "" for normal-risk actions or when no
// subject is resolvable from args. Backend-generated and stored on the
// preview row (spec § 5.4 / hard rule #2) — never supplied by the client.
func RequiredPhrase(action string, args map[string]any) string {
	if RiskLevel(action, args) == RiskNormal {
		return ""
	}
	subject := subjectFromArgs(action, args)
	if subject == "" {
		return ""
	}
	return fmt.Sprintf("DELETE %s", subject)
}

// subjectFromArgs extracts the primary resource identifier per action. v1
// only wires delete_app through the typed-confirmation gate, so only it
// resolves a subject; other catalog actions return "" until they are wired
// (their preview producer will pass the subject in args at that point).
func subjectFromArgs(action string, args map[string]any) string {
	switch action {
	case "delete_app":
		return stringArg(args, "confirm")
	default:
		return ""
	}
}

// planRank orders plan tiers for downgrade detection (ADR-0011).
var planRank = map[string]int{"free": 0, "starter": 1, "pro": 2, "team": 3}

func isPlanDowngrade(args map[string]any) bool {
	from, fromOK := planRank[strings.ToLower(stringArg(args, "from"))]
	to, toOK := planRank[strings.ToLower(stringArg(args, "to"))]
	if !fromOK || !toOK {
		return false
	}
	return to < from
}

func isPrivilegedRole(args map[string]any) bool {
	switch strings.ToLower(stringArg(args, "role")) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
