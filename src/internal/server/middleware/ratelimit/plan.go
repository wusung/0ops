package ratelimit

import "time"

// Plan tier (ADR-0011).
type Plan string

// ADR-0011 § 3.1 plan tiers.
const (
	PlanFree    Plan = "free"
	PlanStarter Plan = "starter"
	PlanPro     Plan = "pro"
	PlanTeam    Plan = "team"
)

// AllPlans lists the four valid plan tiers in ADR-0011 § 3.1.
func AllPlans() []Plan { return []Plan{PlanFree, PlanStarter, PlanPro, PlanTeam} }

// Normalize maps any string into a known Plan; unknown values fall back to
// PlanFree (most conservative quota).
func Normalize(s string) Plan {
	switch Plan(s) {
	case PlanFree, PlanStarter, PlanPro, PlanTeam:
		return Plan(s)
	}
	return PlanFree
}

// Category groups requests for quota accounting.
type Category string

// Categories tracked by the limiter (spec § 4.1).
const (
	CategoryRead          Category = "read"
	CategoryWrite         Category = "write"
	CategoryPreviewCreate Category = "preview_create"
)

// Scope distinguishes per-token vs per-team buckets in metrics + storage.
type Scope string

// Scopes tracked by the limiter (spec § 4.2).
const (
	ScopePerToken Scope = "per_token"
	ScopePerTeam  Scope = "per_team"
)

// Quota holds requests-per-window for one (plan, scope, category) cell.
// Window is always 1 minute in M4.2; per-team build-per-hour quota is deferred.
type Quota struct {
	PerMinute int
}

// PlanQuotas holds all (scope, category) quotas for one plan tier.
type PlanQuotas struct {
	PerTokenRead          Quota
	PerTokenWrite         Quota
	PerTokenPreviewCreate Quota
	PerTeamWrite          Quota
	PerTeamPreviewCreate  Quota
}

// DefaultPlanQuotas returns ADR-0011 § 3.1 numbers (per-minute quotas).
// Per-team build-per-hour is intentionally omitted (M4.2 out-of-scope; see
// docs/superpowers/plans/2026-05-16-m4.2-rate-limit.md "out-of-scope").
func DefaultPlanQuotas() map[Plan]PlanQuotas {
	return map[Plan]PlanQuotas{
		PlanFree: {
			PerTokenRead:          Quota{PerMinute: 300},
			PerTokenWrite:         Quota{PerMinute: 30},
			PerTokenPreviewCreate: Quota{PerMinute: 10},
			PerTeamWrite:          Quota{PerMinute: 60},
			PerTeamPreviewCreate:  Quota{PerMinute: 10},
		},
		PlanStarter: {
			PerTokenRead:          Quota{PerMinute: 600},
			PerTokenWrite:         Quota{PerMinute: 60},
			PerTokenPreviewCreate: Quota{PerMinute: 30},
			PerTeamWrite:          Quota{PerMinute: 300},
			PerTeamPreviewCreate:  Quota{PerMinute: 30},
		},
		PlanPro: {
			PerTokenRead:          Quota{PerMinute: 2400},
			PerTokenWrite:         Quota{PerMinute: 240},
			PerTokenPreviewCreate: Quota{PerMinute: 120},
			PerTeamWrite:          Quota{PerMinute: 1200},
			PerTeamPreviewCreate:  Quota{PerMinute: 120},
		},
		PlanTeam: {
			PerTokenRead:          Quota{PerMinute: 6000},
			PerTokenWrite:         Quota{PerMinute: 600},
			PerTokenPreviewCreate: Quota{PerMinute: 300},
			PerTeamWrite:          Quota{PerMinute: 3000},
			PerTeamPreviewCreate:  Quota{PerMinute: 300},
		},
	}
}

// QuotaFor returns the quota cell for (plan, scope, category). Returns
// Quota{} (PerMinute=0, treated as "no limit") if no cell applies — for
// example per-team read; spec § 4.1 only enforces per-team for write +
// preview_create.
func QuotaFor(quotas map[Plan]PlanQuotas, plan Plan, scope Scope, cat Category) Quota {
	tier, ok := quotas[plan]
	if !ok {
		tier = quotas[PlanFree]
	}
	switch scope {
	case ScopePerToken:
		switch cat {
		case CategoryRead:
			return tier.PerTokenRead
		case CategoryWrite:
			return tier.PerTokenWrite
		case CategoryPreviewCreate:
			return tier.PerTokenPreviewCreate
		}
	case ScopePerTeam:
		switch cat {
		case CategoryWrite:
			return tier.PerTeamWrite
		case CategoryPreviewCreate:
			return tier.PerTeamPreviewCreate
		}
	}
	return Quota{}
}

// Window is the rolling window; M4.2 uses one minute for all categories.
const Window = time.Minute
