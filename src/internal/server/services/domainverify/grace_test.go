package domainverify

import (
	"testing"
	"time"
)

func TestGraceDecisionFirstFailureMarks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	got := EvaluateGrace(GraceInput{
		Now:                 now,
		DNSPasses:           false,
		HealthCheckFailedAt: nil,
	})
	if got.Action != GraceMarkUnhealthy {
		t.Fatalf("got %v, want GraceMarkUnhealthy", got.Action)
	}
	if got.NewFailedAt == nil || !got.NewFailedAt.Equal(now) {
		t.Fatalf("got NewFailedAt=%v, want now", got.NewFailedAt)
	}
}

func TestGraceDecisionRecoveryClearsMark(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-24 * time.Hour)
	got := EvaluateGrace(GraceInput{
		Now:                 now,
		DNSPasses:           true,
		HealthCheckFailedAt: &earlier,
	})
	if got.Action != GraceClearMark {
		t.Fatalf("got %v, want GraceClearMark", got.Action)
	}
}

func TestGraceDecisionContinuesWithinWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	failedAt := now.Add(-3 * 24 * time.Hour)
	got := EvaluateGrace(GraceInput{
		Now:                 now,
		DNSPasses:           false,
		HealthCheckFailedAt: &failedAt,
	})
	if got.Action != GraceContinue {
		t.Fatalf("got %v, want GraceContinue", got.Action)
	}
}

func TestGraceDecisionReleasesAfter7Days(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	failedAt := now.Add(-(7*24*time.Hour + time.Minute))
	got := EvaluateGrace(GraceInput{
		Now:                 now,
		DNSPasses:           false,
		HealthCheckFailedAt: &failedAt,
	})
	if got.Action != GraceRelease {
		t.Fatalf("got %v, want GraceRelease", got.Action)
	}
}

func TestGraceDecisionNoOpWhenHealthy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	got := EvaluateGrace(GraceInput{
		Now:                 now,
		DNSPasses:           true,
		HealthCheckFailedAt: nil,
	})
	if got.Action != GraceNoOp {
		t.Fatalf("got %v, want GraceNoOp", got.Action)
	}
}
