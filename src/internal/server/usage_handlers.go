package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/wusung/0ops/internal/server/apperror"
	"github.com/wusung/0ops/internal/server/auth"
	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/usage"
	"github.com/wusung/0ops/internal/shared/dto"
)

// defaultUsageWindowDays is how far back a caller sees when they do not
// say. Thirty days covers a billing period's worth of context.
const defaultUsageWindowDays = 29 // inclusive of today → 30 days

// usageQueryService is the read surface the handlers need.
type usageQueryService interface {
	Query(ctx context.Context, teamID, appID string, fromDay, toDay time.Time, withIntervals bool) (dto.UsageResponse, error)
	QueryWithObserved(ctx context.Context, teamID, appID string, fromDay, toDay time.Time, withIntervals bool) (dto.UsageResponse, error)
}

func listTeamUsageHandler(svc usageQueryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, ok := parseUsageWindow(w, r)
		if !ok {
			return
		}
		writeUsage(w, r, svc, auth.TeamID(r.Context()), "", opts)
	}
}

// usageAppLookup resolves an app slug within the caller's team. Narrower
// than appsStore so the handler test does not need the whole store.
type usageAppLookup interface {
	GetTeamAppBySlug(ctx context.Context, teamID string, slug string) (db.App, error)
}

func getAppUsageHandler(store usageAppLookup, svc usageQueryService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, ok := parseUsageWindow(w, r)
		if !ok {
			return
		}
		teamID := auth.TeamID(r.Context())
		slug := chi.URLParam(r, "app_slug")
		row, err := store.GetTeamAppBySlug(r.Context(), teamID, slug)
		if err != nil {
			// A cross-team lookup lands here too, and must be
			// indistinguishable from a missing app.
			if errors.Is(err, pgx.ErrNoRows) {
				apperror.Write(w, "app_not_found", apperror.ClassNotFound, "app not found", map[string]any{
					"app_slug": slug,
				})
				return
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to get app", nil)
			return
		}
		writeUsage(w, r, svc, teamID, row.ID, opts)
	}
}

func writeUsage(w http.ResponseWriter, r *http.Request, svc usageQueryService, teamID, appID string, opts usageWindow) {
	query := svc.Query
	if opts.observed {
		query = svc.QueryWithObserved
	}
	resp, err := query(r.Context(), teamID, appID, opts.from, opts.to, opts.intervals)
	if err != nil {
		if errors.Is(err, db.ErrTooManyIntervals) {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
				"too many allocation intervals in this window; narrow it or drop detail=interval",
				map[string]any{"max_intervals": db.MaxIntervalRows})
			return
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to read usage", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// usageWindow is the parsed query: which days, and how much detail.
type usageWindow struct {
	from      time.Time
	to        time.Time
	intervals bool
	observed  bool
}

// parseUsageWindow reads from / to / detail / include. It writes the
// error response itself and reports whether the caller should continue.
func parseUsageWindow(w http.ResponseWriter, r *http.Request) (usageWindow, bool) {
	var out usageWindow
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := today.AddDate(0, 0, -defaultUsageWindowDays)
	to := today

	q := r.URL.Query()
	if raw := q.Get("from"); raw != "" {
		parsed, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
				"from must be a UTC date in YYYY-MM-DD form", map[string]any{"from": raw})
			return usageWindow{}, false
		}
		from = parsed.UTC()
	}
	if raw := q.Get("to"); raw != "" {
		parsed, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
				"to must be a UTC date in YYYY-MM-DD form", map[string]any{"to": raw})
			return usageWindow{}, false
		}
		to = parsed.UTC()
	}
	if to.Before(from) {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
			"to must not be before from", map[string]any{
				"from": from.Format(time.DateOnly), "to": to.Format(time.DateOnly)})
		return usageWindow{}, false
	}

	switch detail := q.Get("detail"); detail {
	case "":
	case "interval":
		// Raw intervals are kept for 13 months; past that only daily
		// totals exist, so say so rather than return a silently empty
		// list that reads like "no usage".
		oldest := today.AddDate(0, 0, -usage.IntervalRetentionDays)
		if from.Before(oldest) {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
				"interval detail is only retained for 13 months; use the default daily view for older windows",
				map[string]any{
					"from":               from.Format(time.DateOnly),
					"oldest_available":   oldest.Format(time.DateOnly),
					"retention_days":     usage.IntervalRetentionDays,
					"suggested_fallback": "omit detail=interval",
				})
			return usageWindow{}, false
		}
		out.intervals = true
	default:
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
			`detail must be "interval" when set`, map[string]any{"detail": detail})
		return usageWindow{}, false
	}

	switch include := q.Get("include"); include {
	case "":
	case "observed":
		// Observations live only as long as the sample retention window,
		// and only exist where metrics-server runs. Rather than reject
		// an older window, serve it with the observed block simply
		// absent — absent means "not measured", not "measured zero".
		out.observed = true
	default:
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest,
			`include must be "observed" when set`, map[string]any{"include": include})
		return usageWindow{}, false
	}

	out.from, out.to = from, to
	return out, true
}
