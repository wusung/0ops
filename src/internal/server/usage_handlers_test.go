package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/wusung/0ops/internal/server/auth"
	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/shared/dto"
)

type stubUsageService struct {
	gotTeamID   string
	gotAppID    string
	gotFrom     time.Time
	gotTo       time.Time
	gotDetail   bool
	gotObserved bool
	resp        dto.UsageResponse
}

func (s *stubUsageService) Query(_ context.Context, teamID, appID string, from, to time.Time, detail bool) (dto.UsageResponse, error) {
	s.gotTeamID, s.gotAppID, s.gotFrom, s.gotTo, s.gotDetail = teamID, appID, from, to, detail
	return s.resp, nil
}

func (s *stubUsageService) QueryWithObserved(ctx context.Context, teamID, appID string, from, to time.Time, detail bool) (dto.UsageResponse, error) {
	s.gotObserved = true
	return s.Query(ctx, teamID, appID, from, to, detail)
}

func TestTeamUsageDefaultsToThirtyDays(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if svc.gotTeamID != "team-1" {
		t.Errorf("team not taken from the authenticated context: %q", svc.gotTeamID)
	}
	if days := svc.gotTo.Sub(svc.gotFrom).Hours() / 24; days != 29 {
		t.Errorf("default window spans %v days + today, want 29", days)
	}
}

func TestTeamUsageParsesExplicitWindow(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?from=2026-09-01&to=2026-09-07", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if svc.gotFrom.Format(time.DateOnly) != "2026-09-01" || svc.gotTo.Format(time.DateOnly) != "2026-09-07" {
		t.Errorf("window = %v..%v", svc.gotFrom, svc.gotTo)
	}
}

func TestUsageRejectsMalformedDates(t *testing.T) {
	for _, q := range []string{"?from=yesterday", "?to=2026-13-45", "?from=2026-09-10&to=2026-09-01"} {
		svc := &stubUsageService{}
		req := httptest.NewRequest("GET", "/v1/teams/acme/usage"+q, nil)
		req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
		rec := httptest.NewRecorder()

		listTeamUsageHandler(svc)(rec, req)
		if rec.Code != 400 {
			t.Errorf("%s → status %d, want 400", q, rec.Code)
		}
	}
}

// Beyond retention there is no interval detail, only daily totals. An
// empty list would read as "no usage", which is a different claim.
func TestIntervalDetailBeyondRetentionIsRejectedWithGuidance(t *testing.T) {
	svc := &stubUsageService{}
	old := time.Now().UTC().AddDate(0, 0, -400).Format(time.DateOnly)
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?detail=interval&from="+old, nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "oldest_available") {
		t.Errorf("error must tell the caller what window is still available: %s", rec.Body)
	}
}

func TestIntervalDetailWithinRetentionIsForwarded(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?detail=interval", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if rec.Code != 200 || !svc.gotDetail {
		t.Fatalf("status=%d detail=%v", rec.Code, svc.gotDetail)
	}
}

func TestUnknownDetailValueRejected(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?detail=raw", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

type stubAppLookup struct {
	app db.App
	err error
}

func (s stubAppLookup) GetTeamAppBySlug(context.Context, string, string) (db.App, error) {
	return s.app, s.err
}

// An app in another team must be indistinguishable from one that does
// not exist.
func TestAppUsageCrossTeamLooksLikeNotFound(t *testing.T) {
	svc := &stubUsageService{}
	h := getAppUsageHandler(stubAppLookup{err: pgx.ErrNoRows}, svc)

	req := httptest.NewRequest("GET", "/v1/teams/acme/apps/other/usage", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("app_slug", "other")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if svc.gotAppID != "" {
		t.Error("usage was queried for an app the caller cannot see")
	}
}

func TestAppUsageScopesToResolvedApp(t *testing.T) {
	svc := &stubUsageService{}
	h := getAppUsageHandler(stubAppLookup{app: db.App{ID: "app-9"}}, svc)

	req := httptest.NewRequest("GET", "/v1/teams/acme/apps/web/usage", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("app_slug", "web")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body)
	}
	if svc.gotAppID != "app-9" || svc.gotTeamID != "team-1" {
		t.Errorf("query scoped to team=%q app=%q", svc.gotTeamID, svc.gotAppID)
	}
}

func TestIncludeObservedRoutesToTheObservationQuery(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?include=observed", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if rec.Code != 200 || !svc.gotObserved {
		t.Fatalf("status=%d observed=%v", rec.Code, svc.gotObserved)
	}
}

func TestUsageOmitsObservationUnlessAsked(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if svc.gotObserved {
		t.Error("observation costs an extra query; it must be opt-in")
	}
}

func TestUnknownIncludeValueRejected(t *testing.T) {
	svc := &stubUsageService{}
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?include=everything", nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// An old window has no samples left, but that is not an error: the
// allocation figures are still exact, and the observed block is simply
// absent. Rejecting would deny the caller data that does exist.
func TestObservedBeyondSampleRetentionStillServesAllocation(t *testing.T) {
	svc := &stubUsageService{}
	old := time.Now().UTC().AddDate(0, 0, -200).Format(time.DateOnly)
	req := httptest.NewRequest("GET", "/v1/teams/acme/usage?include=observed&from="+old, nil)
	req = req.WithContext(auth.WithTeamIDForTest(req.Context(), "team-1"))
	rec := httptest.NewRecorder()

	listTeamUsageHandler(svc)(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
}
