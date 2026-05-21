package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/middleware/ratelimit"
)

// --- fakeQuotaStore: in-memory stub for uploadQuotaStore ---

type fakeQuotaStore struct {
	inertBytes  int64
	pinned      int
	dailyCount  int
	returnErr   error
}

func (f *fakeQuotaStore) SumInertBytesByTeam(_ context.Context, _ string) (int64, error) {
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	return f.inertBytes, nil
}

func (f *fakeQuotaStore) CountPinnedByTeam(_ context.Context, _ string) (int, error) {
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	return f.pinned, nil
}

func (f *fakeQuotaStore) CountTeamUploadsSince(_ context.Context, _ string, _ time.Time) (int, error) {
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	return f.dailyCount, nil
}

// fixedNow returns a deterministic time.Now() func for tests.
func fixedNow() func() time.Time {
	t := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// freeTier returns the PlanFree UploadQuotaTier from DefaultUploadQuotas().
func freeTier() UploadQuotaTier {
	return DefaultUploadQuotas()[ratelimit.PlanFree]
}

func TestCheckUploadQuota_HappyPath(t *testing.T) {
	tier := freeTier()
	store := &fakeQuotaStore{
		pinned:     tier.MaxConcurrentPinned - 1,
		dailyCount: tier.MaxDailyUploads - 1,
		inertBytes: 0,
	}
	err := checkUploadQuota(
		context.Background(),
		store,
		DefaultUploadQuotas(),
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestCheckUploadQuota_RejectsInertBytesCap(t *testing.T) {
	tier := freeTier()
	// inert + DefaultUploadMaxArchiveBytes > cap
	store := &fakeQuotaStore{
		pinned:     0,
		dailyCount: 0,
		inertBytes: tier.MaxInertBytes - DefaultUploadMaxArchiveBytes + 1, // pushes over the edge
	}
	err := checkUploadQuota(
		context.Background(),
		store,
		DefaultUploadQuotas(),
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if err == nil {
		t.Fatal("expected quota error, got nil")
	}
	if !IsQuotaExceeded(err) {
		t.Errorf("expected IsQuotaExceeded=true, got false; err=%v", err)
	}
	var qe *quotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *quotaError, got %T", err)
	}
	if qe.Reason == "" {
		t.Error("quotaError.Reason should not be empty")
	}
	// Reason must mention "inert bytes"
	if len(qe.Reason) == 0 {
		t.Error("quotaError.Reason is empty")
	}
	// Check that the error mentions the inert bytes dimension
	if !strings.Contains(qe.Reason, "inert bytes") {
		t.Errorf("quotaError.Reason = %q, want 'inert bytes' mention", qe.Reason)
	}
}

func TestCheckUploadQuota_RejectsConcurrentPinnedCap(t *testing.T) {
	tier := freeTier()
	store := &fakeQuotaStore{
		pinned:     tier.MaxConcurrentPinned, // at cap
		dailyCount: 0,
		inertBytes: 0,
	}
	err := checkUploadQuota(
		context.Background(),
		store,
		DefaultUploadQuotas(),
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if !IsQuotaExceeded(err) {
		t.Fatalf("expected quota exceeded, got %v", err)
	}
	var qe *quotaError
	errors.As(err, &qe)
	if !strings.Contains(qe.Reason, "concurrent pinned") {
		t.Errorf("quotaError.Reason = %q, want 'concurrent pinned' mention", qe.Reason)
	}
}

func TestCheckUploadQuota_RejectsDailyUploadCap(t *testing.T) {
	tier := freeTier()
	store := &fakeQuotaStore{
		pinned:     0,
		dailyCount: tier.MaxDailyUploads, // at cap
		inertBytes: 0,
	}
	err := checkUploadQuota(
		context.Background(),
		store,
		DefaultUploadQuotas(),
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if !IsQuotaExceeded(err) {
		t.Fatalf("expected quota exceeded, got %v", err)
	}
	var qe *quotaError
	errors.As(err, &qe)
	if !strings.Contains(qe.Reason, "daily upload") {
		t.Errorf("quotaError.Reason = %q, want 'daily upload' mention", qe.Reason)
	}
}

func TestCheckUploadQuota_NilStoreSkipsCheck(t *testing.T) {
	err := checkUploadQuota(
		context.Background(),
		nil, // nil store
		DefaultUploadQuotas(),
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if err != nil {
		t.Errorf("nil store: expected nil error, got %v", err)
	}
}

func TestCheckUploadQuota_NilTiersSkipsCheck(t *testing.T) {
	store := &fakeQuotaStore{}
	err := checkUploadQuota(
		context.Background(),
		store,
		nil, // nil tiers
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if err != nil {
		t.Errorf("nil tiers: expected nil error, got %v", err)
	}
}

func TestCheckUploadQuota_PlanFallback(t *testing.T) {
	// Unknown plan should fall back to PlanFree (most conservative caps)
	unknownPlan := ratelimit.Plan("enterprise-ultra")
	freeTierCaps := freeTier()

	store := &fakeQuotaStore{
		pinned:     freeTierCaps.MaxConcurrentPinned, // at Free cap, which fallback should use
		dailyCount: 0,
		inertBytes: 0,
	}
	err := checkUploadQuota(
		context.Background(),
		store,
		DefaultUploadQuotas(),
		"team-1",
		unknownPlan,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	// Should be rejected because unknown plan falls back to free caps
	if !IsQuotaExceeded(err) {
		t.Errorf("unknown plan should fall back to PlanFree caps and reject; got err=%v", err)
	}
}

func TestCheckUploadQuota_DBErrorPropagates(t *testing.T) {
	dbErr := errors.New("connection refused")
	store := &fakeQuotaStore{returnErr: dbErr}
	err := checkUploadQuota(
		context.Background(),
		store,
		DefaultUploadQuotas(),
		"team-1",
		ratelimit.PlanFree,
		DefaultUploadMaxArchiveBytes,
		fixedNow(),
	)
	if err == nil {
		t.Fatal("expected error from DB failure, got nil")
	}
	if IsQuotaExceeded(err) {
		t.Error("DB error should NOT be a quotaError (IsQuotaExceeded should be false)")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected err to wrap dbErr, got %v", err)
	}
}

func TestIsQuotaExceeded_DistinguishesQuotaFromOther(t *testing.T) {
	qErr := &quotaError{Reason: "some quota reason"}
	otherErr := errors.New("some other error")

	if !IsQuotaExceeded(qErr) {
		t.Error("IsQuotaExceeded(*quotaError) should return true")
	}
	if IsQuotaExceeded(otherErr) {
		t.Error("IsQuotaExceeded(other error) should return false")
	}
	if IsQuotaExceeded(nil) {
		t.Error("IsQuotaExceeded(nil) should return false")
	}
}

func TestDefaultUploadQuotas_HasAllFourPlans(t *testing.T) {
	quotas := DefaultUploadQuotas()

	plans := []ratelimit.Plan{ratelimit.PlanFree, ratelimit.PlanStarter, ratelimit.PlanPro, ratelimit.PlanTeam}
	for _, p := range plans {
		tier, ok := quotas[p]
		if !ok {
			t.Errorf("plan %q missing from DefaultUploadQuotas", p)
			continue
		}
		if tier.MaxInertBytes <= 0 {
			t.Errorf("plan %q: MaxInertBytes should be positive, got %d", p, tier.MaxInertBytes)
		}
		if tier.MaxConcurrentPinned <= 0 {
			t.Errorf("plan %q: MaxConcurrentPinned should be positive, got %d", p, tier.MaxConcurrentPinned)
		}
		if tier.MaxDailyUploads <= 0 {
			t.Errorf("plan %q: MaxDailyUploads should be positive, got %d", p, tier.MaxDailyUploads)
		}
	}

	// Free must be the most conservative
	free := quotas[ratelimit.PlanFree]
	for _, p := range []ratelimit.Plan{ratelimit.PlanStarter, ratelimit.PlanPro, ratelimit.PlanTeam} {
		tier := quotas[p]
		if tier.MaxInertBytes <= free.MaxInertBytes {
			t.Errorf("plan %q MaxInertBytes (%d) should be > Free (%d)", p, tier.MaxInertBytes, free.MaxInertBytes)
		}
		if tier.MaxConcurrentPinned <= free.MaxConcurrentPinned {
			t.Errorf("plan %q MaxConcurrentPinned (%d) should be > Free (%d)", p, tier.MaxConcurrentPinned, free.MaxConcurrentPinned)
		}
		if tier.MaxDailyUploads <= free.MaxDailyUploads {
			t.Errorf("plan %q MaxDailyUploads (%d) should be > Free (%d)", p, tier.MaxDailyUploads, free.MaxDailyUploads)
		}
	}
}

func TestCheckUploadQuota_NearCapSmallUploadStillRejected(t *testing.T) {
	// Spec §11 reserve-max model: inert + maxArchive > cap → reject, even
	// if the actual upload would be tiny. This is the deliberate v1 trade-off:
	// strict but simple. Without this test, a future refactor "fixing" the
	// small-upload case would silently bypass quota enforcement near cap.
	store := &fakeQuotaStore{inertBytes: 950 * 1024 * 1024} // 950 MiB inert
	quotas := map[ratelimit.Plan]UploadQuotaTier{
		ratelimit.PlanFree: {
			MaxInertBytes:       1 * 1024 * 1024 * 1024, // 1 GiB cap
			MaxConcurrentPinned: 1000,
			MaxDailyUploads:     1000,
		},
	}
	// Even though the actual upload might be just 1 byte, the reserve-max
	// model reserves the full DefaultUploadMaxArchiveBytes (100 MiB).
	// 950 MiB + 100 MiB > 1 GiB cap → reject.
	err := checkUploadQuota(context.Background(), store, quotas, "team-1",
		ratelimit.PlanFree, DefaultUploadMaxArchiveBytes, fixedNow())
	if !IsQuotaExceeded(err) {
		t.Fatalf("expected quota rejection, got %v", err)
	}
	var qe *quotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *quotaError, got %T", err)
	}
	if !strings.Contains(qe.Reason, "inert bytes") {
		t.Errorf("expected reason to mention 'inert bytes', got %q", qe.Reason)
	}
}
