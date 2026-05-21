package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/winshare/zeroops/internal/server/middleware/ratelimit"
)

// UploadQuotaTier captures the team-level upload caps for one plan tier.
// Distinct from ratelimit.PlanQuotas which governs per-minute request rates.
type UploadQuotaTier struct {
	// MaxInertBytes caps the sum of non-gc'd, non-expired upload sizes (bytes).
	MaxInertBytes int64
	// MaxConcurrentPinned caps the number of concurrently-pinned uploads.
	MaxConcurrentPinned int
	// MaxDailyUploads caps the number of upload creations in the last 24 hours.
	MaxDailyUploads int
}

// DefaultUploadQuotas returns spec §11 default caps per plan tier.
func DefaultUploadQuotas() map[ratelimit.Plan]UploadQuotaTier {
	return map[ratelimit.Plan]UploadQuotaTier{
		ratelimit.PlanFree: {
			MaxInertBytes:       1 * 1024 * 1024 * 1024, // 1 GB
			MaxConcurrentPinned: 50,
			MaxDailyUploads:     200,
		},
		ratelimit.PlanStarter: {
			MaxInertBytes:       2 * 1024 * 1024 * 1024, // 2 GB
			MaxConcurrentPinned: 100,
			MaxDailyUploads:     500,
		},
		ratelimit.PlanPro: {
			MaxInertBytes:       5 * 1024 * 1024 * 1024, // 5 GB
			MaxConcurrentPinned: 500,
			MaxDailyUploads:     2000,
		},
		ratelimit.PlanTeam: {
			MaxInertBytes:       20 * 1024 * 1024 * 1024, // 20 GB
			MaxConcurrentPinned: 2000,
			MaxDailyUploads:     10000,
		},
	}
}

// quotaTierFor returns the tier for the team's plan; falls back to PlanFree
// (most conservative documented caps).
//
// If PlanFree is also absent from the map — e.g. a misconfigured self-hosted
// override that omits the Free tier — this returns the zero UploadQuotaTier
// (all caps = 0). This is intentionally conservative: every upload will be
// rejected with a "near cap" error rather than silently bypass enforcement.
// Self-hosted operators must ensure PlanFree exists in any custom map.
func quotaTierFor(quotas map[ratelimit.Plan]UploadQuotaTier, plan ratelimit.Plan) UploadQuotaTier {
	if tier, ok := quotas[plan]; ok {
		return tier
	}
	return quotas[ratelimit.PlanFree]
}

// uploadQuotaStore is the DB read interface for quota checks. db.Repository satisfies it.
type uploadQuotaStore interface {
	SumInertBytesByTeam(ctx context.Context, teamID string) (int64, error)
	CountPinnedByTeam(ctx context.Context, teamID string) (int, error)
	CountTeamUploadsSince(ctx context.Context, teamID string, since time.Time) (int, error)
}

// checkUploadQuota performs the pre-multipart quota guard for POST /v1/teams/{slug}/uploads.
// It runs BEFORE the body is read so a quota-exceeded client doesn't waste bandwidth.
//
// quotaMaxArchiveBytes is the server-configured per-upload size cap (100 MB by
// default). We reserve this as upper bound for the incoming upload: if inert
// + maxArchiveBytes > tier.MaxInertBytes, reject. This is the conservative
// "reserve max" model — strict but simple. A small upload to a near-cap team
// will be rejected; acceptable for v1.
//
// `now` is injected for testing the rolling-window boundary; production
// callers pass time.Now.
//
// TOCTOU note: two concurrent uploads can both pass this check and together
// exceed the cap. Precise enforcement requires a DB-level advisory lock or
// a serialised reservation; deferred post-v1. The server-wide max archive
// size bounds the overshoot per upload.
//
// Returns nil on pass; returns *quotaError on rejection (mapped to apperror
// by the caller).
func checkUploadQuota(
	ctx context.Context,
	store uploadQuotaStore,
	quotas map[ratelimit.Plan]UploadQuotaTier,
	teamID string,
	plan ratelimit.Plan,
	quotaMaxArchiveBytes int64,
	now func() time.Time,
) error {
	if store == nil || quotas == nil {
		return nil
	}

	tier := quotaTierFor(quotas, plan)

	// 1. Concurrent pinned uploads (cheapest query first)
	pinned, err := store.CountPinnedByTeam(ctx, teamID)
	if err != nil {
		return fmt.Errorf("count pinned: %w", err)
	}
	if pinned >= tier.MaxConcurrentPinned {
		return &quotaError{
			Reason: fmt.Sprintf("concurrent pinned uploads at cap (%d/%d for plan %s)",
				pinned, tier.MaxConcurrentPinned, plan),
		}
	}

	// 2. Daily upload count (rolling 24-hour window)
	daily, err := store.CountTeamUploadsSince(ctx, teamID, now().Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("count daily uploads: %w", err)
	}
	if daily >= tier.MaxDailyUploads {
		return &quotaError{
			Reason: fmt.Sprintf("daily upload count at cap (%d/%d for plan %s)",
				daily, tier.MaxDailyUploads, plan),
		}
	}

	// 3. Inert bytes — reserve quotaMaxArchiveBytes as upper bound
	inert, err := store.SumInertBytesByTeam(ctx, teamID)
	if err != nil {
		return fmt.Errorf("sum inert bytes: %w", err)
	}
	if inert+quotaMaxArchiveBytes > tier.MaxInertBytes {
		return &quotaError{
			Reason: fmt.Sprintf("inert bytes near cap (%d + max %d > %d for plan %s)",
				inert, quotaMaxArchiveBytes, tier.MaxInertBytes, plan),
		}
	}

	return nil
}

// quotaError is the internal sentinel returned by checkUploadQuota.
// Handler maps to apperror.CodeTeamQuotaExceeded (422 + ClassUnprocessable).
type quotaError struct {
	Reason string
}

func (e *quotaError) Error() string { return "upload quota exceeded: " + e.Reason }

// IsQuotaExceeded checks if an error indicates quota rejection.
func IsQuotaExceeded(err error) bool {
	var qe *quotaError
	return errors.As(err, &qe)
}
