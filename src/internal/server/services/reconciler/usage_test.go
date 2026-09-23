package reconciler

import (
	"context"
	"errors"
	"testing"
)

type stubUsageScanner struct {
	calls  int
	opened int
	closed int
	err    error
}

func (s *stubUsageScanner) Tick(context.Context) (int, int, error) {
	s.calls++
	return s.opened, s.closed, s.err
}

// A follower applying the same plan would race the leader over which end
// time wins on a close.
func TestUsageReconcileSkippedUnderFollowerGate(t *testing.T) {
	scanner := &stubUsageScanner{opened: 1}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(false), Observer: obs, UsageScanner: scanner})

	runner.runUsageReconcile(context.Background())

	if scanner.calls != 0 {
		t.Fatalf("follower ran the ledger reconcile %d times", scanner.calls)
	}
	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_reconcile:skipped_not_leader" {
		t.Fatalf("ticks = %v", obs.ticks)
	}
}

func TestUsageReconcileRecordsOutcome(t *testing.T) {
	scanner := &stubUsageScanner{opened: 2, closed: 1}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(true), Observer: obs, UsageScanner: scanner})

	runner.runUsageReconcile(context.Background())

	if scanner.calls != 1 {
		t.Fatalf("scanner calls = %d, want 1", scanner.calls)
	}
	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_reconcile:success" {
		t.Fatalf("ticks = %v", obs.ticks)
	}
}

func TestUsageReconcileReportsFailure(t *testing.T) {
	scanner := &stubUsageScanner{err: errors.New("api server down")}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(true), Observer: obs, UsageScanner: scanner})

	runner.runUsageReconcile(context.Background())

	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_reconcile:error" {
		t.Fatalf("a failed pass must be visible as an error tick, got %v", obs.ticks)
	}
}

// Without a scanner the loop must not spawn at all, so a deployment that
// has not wired the ledger does not log an error every five minutes.
func TestUsageLoopNotSpawnedWhenUnconfigured(t *testing.T) {
	runner := New(Config{Leader: stubLeader(true), Observer: newRecordingObserver()})
	if runner.cfg.UsageScanner != nil {
		t.Fatal("precondition")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Start(ctx)
	runner.Wait()
}

// Default cadence is documented in the spec; a silent change to it would
// widen the worst-case under-bill on a vanished pod.
func TestUsageIntervalDefaultsToFiveMinutes(t *testing.T) {
	runner := New(Config{UsageScanner: &stubUsageScanner{}})
	if got := runner.cfg.UsageInterval.Minutes(); got != 5 {
		t.Fatalf("UsageInterval = %v minutes, want 5", got)
	}
}

type stubRollupScanner struct {
	calls int
	days  int
	err   error
}

func (s *stubRollupScanner) Tick(context.Context) (int, error) {
	s.calls++
	return s.days, s.err
}

func TestUsageRollupSkippedUnderFollowerGate(t *testing.T) {
	scanner := &stubRollupScanner{days: 3}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(false), Observer: obs, UsageRollupScanner: scanner})

	runner.runUsageRollup(context.Background())

	if scanner.calls != 0 {
		t.Fatalf("follower rolled up %d times", scanner.calls)
	}
	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_rollup:skipped_not_leader" {
		t.Fatalf("ticks = %v", obs.ticks)
	}
}

func TestUsageRollupRecordsOutcome(t *testing.T) {
	scanner := &stubRollupScanner{days: 2}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(true), Observer: obs, UsageRollupScanner: scanner})

	runner.runUsageRollup(context.Background())

	if scanner.calls != 1 || len(obs.ticks) != 1 || obs.ticks[0] != "usage_rollup:success" {
		t.Fatalf("calls=%d ticks=%v", scanner.calls, obs.ticks)
	}
}

func TestUsageRollupReportsFailure(t *testing.T) {
	scanner := &stubRollupScanner{err: errors.New("db down")}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(true), Observer: obs, UsageRollupScanner: scanner})

	runner.runUsageRollup(context.Background())

	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_rollup:error" {
		t.Fatalf("ticks = %v", obs.ticks)
	}
}

// Hourly, not daily: a backend that was down at midnight must still close
// the books within the hour rather than waiting a full day.
func TestUsageRollupIntervalDefaultsToOneHour(t *testing.T) {
	runner := New(Config{UsageRollupScanner: &stubRollupScanner{}})
	if got := runner.cfg.UsageRollupInterval.Hours(); got != 1 {
		t.Fatalf("UsageRollupInterval = %v hours, want 1", got)
	}
}

type stubSampleScanner struct {
	calls    int
	written  int
	failures int
	err      error
}

func (s *stubSampleScanner) Tick(context.Context) (int, error) {
	s.calls++
	return s.written, s.err
}
func (s *stubSampleScanner) ConsecutiveFailures() int { return s.failures }

func TestUsageSampleSkippedUnderFollowerGate(t *testing.T) {
	scanner := &stubSampleScanner{written: 3}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(false), Observer: obs, UsageSampleScanner: scanner})

	runner.runUsageSample(context.Background())

	if scanner.calls != 0 {
		t.Fatalf("follower sampled %d times", scanner.calls)
	}
	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_sample:skipped_not_leader" {
		t.Fatalf("ticks = %v", obs.ticks)
	}
}

// A missing metrics-server degrades observation only. Reporting it as an
// error would put it alongside metering failures, which are a different
// severity entirely.
func TestUsageSampleReportsDegradedNotError(t *testing.T) {
	scanner := &stubSampleScanner{failures: 4}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(true), Observer: obs, UsageSampleScanner: scanner})

	runner.runUsageSample(context.Background())

	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_sample:degraded" {
		t.Fatalf("ticks = %v, want a degraded tick", obs.ticks)
	}
}

func TestUsageSampleRecordsSuccess(t *testing.T) {
	scanner := &stubSampleScanner{written: 2}
	obs := newRecordingObserver()
	runner := New(Config{Leader: stubLeader(true), Observer: obs, UsageSampleScanner: scanner})

	runner.runUsageSample(context.Background())

	if len(obs.ticks) != 1 || obs.ticks[0] != "usage_sample:success" {
		t.Fatalf("ticks = %v", obs.ticks)
	}
}

// The observation track is optional; a deployment without it must not
// spawn a loop that logs every five minutes.
func TestUsageSampleLoopNotSpawnedWhenUnconfigured(t *testing.T) {
	runner := New(Config{Leader: stubLeader(true), Observer: newRecordingObserver()})
	if runner.cfg.UsageSampleScanner != nil {
		t.Fatal("precondition")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Start(ctx)
	runner.Wait()
}
