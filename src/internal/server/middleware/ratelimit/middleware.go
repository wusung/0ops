package ratelimit

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
)

// Middleware enforces per-token + per-team rate limits via a chi-compatible
// http.Handler chain. Must run AFTER auth.Bearer (token id required) and
// after auth.ResolveTeam (team id + plan required for team-scoped routes).
type Middleware struct {
	limiter  *Limiter
	observer Observer
}

// NewMiddleware constructs a Middleware. observer may be NoopObserver{}.
func NewMiddleware(limiter *Limiter, observer Observer) *Middleware {
	if observer == nil {
		observer = NoopObserver{}
	}
	return &Middleware{limiter: limiter, observer: observer}
}

// Handler wraps next with the rate-limit chain.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenID := auth.TokenID(r.Context())
		if tokenID == "" {
			next.ServeHTTP(w, r)
			return
		}
		cat := classify(r.Method, r.URL.Path)
		plan := planFromContext(r.Context())

		if ok, retryAfter := m.limiter.Allow(ScopePerToken, tokenID, plan, cat); !ok {
			m.deny(w, ScopePerToken, cat, plan, retryAfter)
			return
		}
		teamID := auth.TeamID(r.Context())
		if teamID != "" {
			if ok, retryAfter := m.limiter.Allow(ScopePerTeam, teamID, plan, cat); !ok {
				m.deny(w, ScopePerTeam, cat, plan, retryAfter)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) deny(w http.ResponseWriter, scope Scope, cat Category, plan Plan, retryAfter time.Duration) {
	seconds := int(retryAfter.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	q := QuotaFor(m.limiter.cfg.Quotas, plan, scope, cat)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	m.observer.RecordTriggered(scope, cat, plan)
	apperror.Write(w, "rate_limited", apperror.ClassTooManyRequests,
		"rate limit exceeded for "+string(scope)+" ("+string(cat)+" plan="+string(plan)+")",
		map[string]any{
			"scope":         string(scope),
			"category":      string(cat),
			"limit":         q.PerMinute,
			"window_s":      int(Window.Seconds()),
			"retry_after_s": seconds,
			"plan":          string(plan),
		})
}

// classify derives the rate-limit category from method + path.
//
//   - GET / HEAD                                   -> read
//   - POST/PUT/PATCH/DELETE with ":preview" in path -> preview_create
//   - POST/PUT/PATCH/DELETE otherwise                -> write
func classify(method, path string) Category {
	if method == http.MethodGet || method == http.MethodHead {
		return CategoryRead
	}
	if strings.Contains(path, ":preview") {
		return CategoryPreviewCreate
	}
	return CategoryWrite
}

// planFromContext prefers the resolved-team plan; falls back to free.
func planFromContext(ctx context.Context) Plan {
	if p := auth.TeamPlan(ctx); p != "" {
		return Normalize(p)
	}
	return PlanFree
}
