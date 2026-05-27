package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/auth"
)

func newReq(method, path, tokenID, teamID, plan string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := auth.WithTokenIDForTest(r.Context(), tokenID)
	ctx = auth.WithTeamIDForTest(ctx, teamID)
	ctx = auth.WithTeamPlanForTest(ctx, plan)
	return r.WithContext(ctx)
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestMiddlewareCategorization(t *testing.T) {
	cases := []struct {
		method, path string
		want         Category
	}{
		{"GET", "/v1/teams/acme/apps", CategoryRead},
		{"POST", "/v1/teams/acme/apps:preview", CategoryPreviewCreate},
		{"POST", "/v1/teams/acme/apps", CategoryWrite},
		{"POST", "/v1/teams/acme/members:preview-invite", CategoryPreviewCreate},
		{"POST", "/v1/teams/acme/members:invite", CategoryWrite},
		{"PATCH", "/v1/me/auth/tool-grants", CategoryWrite},
		{"DELETE", "/v1/teams/acme/tokens/foo", CategoryWrite},
	}
	for _, c := range cases {
		got := classify(c.method, c.path)
		if got != c.want {
			t.Errorf("classify(%s, %s) = %s, want %s", c.method, c.path, got, c.want)
		}
	}
}

func TestMiddlewareReturns429WithRetryAfterAndEnvelope(t *testing.T) {
	lim := New(Config{Quotas: smallTestQuotas()})
	mw := NewMiddleware(lim, NoopObserver{}).Handler(okHandler())

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, newReq("POST", "/v1/teams/acme/apps", "tok-1", "team-1", "free"))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("call #%d: code = %d, want 204", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, newReq("POST", "/v1/teams/acme/apps", "tok-1", "team-1", "free"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected JSON content-type, got %q", rec.Header().Get("Content-Type"))
	}
	retryAfter, _ := strconv.Atoi(rec.Header().Get("Retry-After"))
	if retryAfter < 1 {
		t.Fatalf("Retry-After = %q, want >= 1", rec.Header().Get("Retry-After"))
	}
	body := rec.Body.String()
	for _, frag := range []string{
		`"code":"rate_limited"`,
		`"scope":"per_token"`,
		`"category":"write"`,
		`"plan":"free"`,
	} {
		if !strings.Contains(body, frag) {
			t.Fatalf("body missing %q: %s", frag, body)
		}
	}
}

func TestMiddlewarePerTeamLimitTriggers(t *testing.T) {
	lim := New(Config{Quotas: smallTestQuotas()})
	mw := NewMiddleware(lim, NoopObserver{}).Handler(okHandler())

	// Two distinct tokens, same team. Per-token write quota = 2 each.
	// Per-team write quota = 3.
	calls := []string{"tok-A", "tok-A", "tok-B"}
	for i, tok := range calls {
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, newReq("POST", "/v1/teams/acme/apps", tok, "team-1", "free"))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("call #%d (tok=%s) code = %d, want 204", i+1, tok, rec.Code)
		}
	}
	// 4th call from tok-B; per-team bucket already drained → 429 with scope=per_team.
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, newReq("POST", "/v1/teams/acme/apps", "tok-B", "team-1", "free"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected per-team 429 on call #4, got %d", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"scope":"per_team"`) {
		t.Fatalf("expected scope=per_team in details, got %s", got)
	}
}

func TestMiddlewareSkipsWhenNoToken(t *testing.T) {
	lim := New(Config{Quotas: smallTestQuotas()})
	mw := NewMiddleware(lim, NoopObserver{}).Handler(okHandler())
	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/auth/device/start", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unauth route should bypass limiter; got %d", rec.Code)
		}
	}
}

func TestMiddlewareIncrementsObserverOn429(t *testing.T) {
	lim := New(Config{Quotas: smallTestQuotas()})
	rec := &countingObserver{}
	mw := NewMiddleware(lim, rec).Handler(okHandler())
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, newReq("POST", "/v1/teams/acme/apps", "tok-1", "team-1", "free"))
	}
	if rec.count == 0 {
		t.Fatalf("expected observer to be called on at least one 429")
	}
}

func TestMiddlewareReadCategoryUsesReadBucket(t *testing.T) {
	lim := New(Config{Quotas: smallTestQuotas()})
	mw := NewMiddleware(lim, NoopObserver{}).Handler(okHandler())

	// per-token read = 5; expect first 5 to pass, 6th to 429.
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, newReq("GET", "/v1/teams/acme/apps", "tok-r", "team-r", "free"))
		if w.Code != http.StatusNoContent {
			t.Fatalf("read #%d code = %d, want 204", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, newReq("GET", "/v1/teams/acme/apps", "tok-r", "team-r", "free"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("read #6 code = %d, want 429", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, `"category":"read"`) {
		t.Fatalf("expected category=read in details, got %s", got)
	}
}

type countingObserver struct{ count int }

func (c *countingObserver) RecordTriggered(_ Scope, _ Category, _ Plan) { c.count++ }

func smallTestQuotas() map[Plan]PlanQuotas {
	return map[Plan]PlanQuotas{
		PlanFree: {
			PerTokenRead:          Quota{PerMinute: 5},
			PerTokenWrite:         Quota{PerMinute: 2},
			PerTokenPreviewCreate: Quota{PerMinute: 2},
			PerTeamWrite:          Quota{PerMinute: 3},
			PerTeamPreviewCreate:  Quota{PerMinute: 3},
		},
	}
}
