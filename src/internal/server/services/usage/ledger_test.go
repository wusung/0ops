package usage

import (
	"testing"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

var (
	now   = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	teamA = AppRef{TeamID: "team-a", AppID: "app-a"}
	apps  = map[string]AppRef{"team-acme/web": teamA}
)

func runningPod(uid string, started time.Time) k3s.PodSnapshot {
	return k3s.PodSnapshot{
		UID:           uid,
		Name:          "web-" + uid,
		Namespace:     "team-acme",
		AppSlug:       "web",
		TeamSlug:      "acme",
		StartedAt:     &started,
		CPUMillicores: 100,
		MemoryBytes:   268435456,
	}
}

func TestOpensIntervalWithPodsOwnStartTime(t *testing.T) {
	started := now.Add(-90 * time.Second)
	plan := Reconcile([]k3s.PodSnapshot{runningPod("p1", started)}, nil, apps, now)

	if len(plan.Opens) != 1 {
		t.Fatalf("want 1 open, got %d", len(plan.Opens))
	}
	got := plan.Opens[0]
	if !got.StartedAt.Equal(started) {
		// Using `now` here would silently shorten every interval the
		// backend did not personally witness beginning.
		t.Errorf("StartedAt = %v, want the pod's own %v", got.StartedAt, started)
	}
	if got.TeamID != teamA.TeamID || got.AppID != teamA.AppID {
		t.Errorf("ownership not resolved from labels: %+v", got)
	}
	if got.CPUMillicores != 100 || got.MemoryBytes != 268435456 {
		t.Errorf("allocation not carried over: %+v", got)
	}
}

// The backend can be down while a pod starts. Its start time is still on
// the object when we come back, so nothing is lost.
func TestBackfillsStartTimeAfterDowntime(t *testing.T) {
	started := now.Add(-6 * time.Hour)
	plan := Reconcile([]k3s.PodSnapshot{runningPod("p1", started)}, nil, apps, now)
	if len(plan.Opens) != 1 || !plan.Opens[0].StartedAt.Equal(started) {
		t.Fatalf("start time not backfilled from the pod object: %+v", plan.Opens)
	}
}

func TestAlreadyOpenPodIsTouchedNotReopened(t *testing.T) {
	started := now.Add(-time.Hour)
	open := []db.OpenIntervalRef{{PodUID: "p1", TeamID: teamA.TeamID, AppID: teamA.AppID, StartedAt: started, LastSeenAt: now.Add(-5 * time.Minute)}}

	plan := Reconcile([]k3s.PodSnapshot{runningPod("p1", started)}, open, apps, now)
	if len(plan.Opens) != 0 {
		t.Errorf("reopened a live pod: %+v", plan.Opens)
	}
	if len(plan.Closes) != 0 {
		t.Errorf("closed a live pod: %+v", plan.Closes)
	}
	if len(plan.Touches) != 1 || plan.Touches[0] != "p1" {
		t.Errorf("want p1 touched, got %v", plan.Touches)
	}
}

func TestTerminationBeatsDeletionTimestamp(t *testing.T) {
	started := now.Add(-time.Hour)
	finished := now.Add(-10 * time.Minute)
	deleted := now.Add(-time.Minute)
	pod := runningPod("p1", started)
	pod.TerminatedAt = &finished
	pod.DeletedAt = &deleted

	open := []db.OpenIntervalRef{{PodUID: "p1", StartedAt: started, LastSeenAt: now}}
	plan := Reconcile([]k3s.PodSnapshot{pod}, open, apps, now)

	if len(plan.Closes) != 1 {
		t.Fatalf("want 1 close, got %d", len(plan.Closes))
	}
	c := plan.Closes[0]
	if !c.EndedAt.Equal(finished) {
		// deletionTimestamp says when removal was asked for; finishedAt
		// says when the containers stopped consuming. Billing follows
		// consumption.
		t.Errorf("EndedAt = %v, want finishedAt %v", c.EndedAt, finished)
	}
	if c.Estimated || c.Reason != db.CloseReasonTerminated {
		t.Errorf("authoritative close mis-flagged: %+v", c)
	}
}

func TestDeletionTimestampUsedWhenNotYetTerminated(t *testing.T) {
	started := now.Add(-time.Hour)
	deleted := now.Add(-time.Minute)
	pod := runningPod("p1", started)
	pod.DeletedAt = &deleted

	plan := Reconcile([]k3s.PodSnapshot{pod}, []db.OpenIntervalRef{{PodUID: "p1", StartedAt: started, LastSeenAt: now}}, apps, now)
	if len(plan.Closes) != 1 || !plan.Closes[0].EndedAt.Equal(deleted) || plan.Closes[0].Reason != db.CloseReasonDeleted {
		t.Fatalf("want a deleted-reason close at %v, got %+v", deleted, plan.Closes)
	}
	if plan.Closes[0].Estimated {
		t.Error("deletionTimestamp is authoritative; must not be flagged estimated")
	}
}

// A pod that lived and died between two passes is still on the cluster
// when we look. Opening and closing it in one plan is the only way it
// gets billed at all.
func TestShortLivedPodIsOpenedAndClosedInOnePass(t *testing.T) {
	started := now.Add(-8 * time.Second)
	finished := now.Add(-1 * time.Second)
	pod := runningPod("p1", started)
	pod.TerminatedAt = &finished

	plan := Reconcile([]k3s.PodSnapshot{pod}, nil, apps, now)
	if len(plan.Opens) != 1 || len(plan.Closes) != 1 {
		t.Fatalf("want one open and one close, got %d/%d", len(plan.Opens), len(plan.Closes))
	}
	if d := plan.Closes[0].EndedAt.Sub(plan.Opens[0].StartedAt); d != 7*time.Second {
		t.Errorf("billed lifetime = %v, want 7s", d)
	}
}

// The pod vanished while we were not looking. We cannot know when, so we
// bill up to the last moment we know it was alive.
func TestVanishedPodClosesAtLastSeenAndIsEstimated(t *testing.T) {
	started := now.Add(-2 * time.Hour)
	lastSeen := now.Add(-5 * time.Minute)
	open := []db.OpenIntervalRef{{PodUID: "gone", StartedAt: started, LastSeenAt: lastSeen}}

	plan := Reconcile(nil, open, apps, now)
	if len(plan.Closes) != 1 {
		t.Fatalf("want 1 close, got %d", len(plan.Closes))
	}
	c := plan.Closes[0]
	if !c.EndedAt.Equal(lastSeen) {
		t.Errorf("EndedAt = %v, want last_seen %v", c.EndedAt, lastSeen)
	}
	if !c.Estimated || c.Reason != db.CloseReasonReconciledMissing {
		t.Errorf("fallback close must be flagged estimated: %+v", c)
	}
	if c.EndedAt.After(now) {
		t.Error("fallback close must never bill past the last evidence of life")
	}
}

func TestUnscheduledPodOpensNothing(t *testing.T) {
	pod := runningPod("p1", now)
	pod.StartedAt = nil

	plan := Reconcile([]k3s.PodSnapshot{pod}, nil, apps, now)
	if len(plan.Opens) != 0 {
		t.Errorf("unscheduled pod holds no node resources; must not be billed: %+v", plan.Opens)
	}
	if plan.UnscheduledPods != 1 {
		t.Errorf("UnscheduledPods = %d, want 1", plan.UnscheduledPods)
	}
}

func TestOrphanPodIsCountedNotBilled(t *testing.T) {
	pod := runningPod("p1", now.Add(-time.Minute))
	pod.AppSlug = "deleted-app"

	plan := Reconcile([]k3s.PodSnapshot{pod}, nil, apps, now)
	if len(plan.Opens) != 0 {
		t.Errorf("orphan pod must not be attributed to anyone: %+v", plan.Opens)
	}
	if plan.OrphanPods != 1 {
		t.Errorf("OrphanPods = %d, want 1", plan.OrphanPods)
	}
}

// Rolling update: the old pod's close and the new pod's open must meet,
// leaving neither a gap nor an overlap in what the team is billed.
func TestRollingUpdateHandsOver(t *testing.T) {
	oldStart := now.Add(-time.Hour)
	handover := now.Add(-30 * time.Second)
	oldPod := runningPod("old", oldStart)
	oldPod.TerminatedAt = &handover
	newPod := runningPod("new", handover)

	open := []db.OpenIntervalRef{{PodUID: "old", StartedAt: oldStart, LastSeenAt: now}}
	plan := Reconcile([]k3s.PodSnapshot{oldPod, newPod}, open, apps, now)

	if len(plan.Opens) != 1 || plan.Opens[0].PodUID != "new" {
		t.Fatalf("new pod not opened: %+v", plan.Opens)
	}
	if len(plan.Closes) != 1 || plan.Closes[0].PodUID != "old" {
		t.Fatalf("old pod not closed: %+v", plan.Closes)
	}
	if !plan.Closes[0].EndedAt.Equal(plan.Opens[0].StartedAt) {
		t.Errorf("handover leaves a gap or overlap: closed %v, opened %v",
			plan.Closes[0].EndedAt, plan.Opens[0].StartedAt)
	}
}

// Defensive: a clock skew that puts the end before the start would make
// the interval negative, which the DB check constraint would reject.
func TestEndNeverPrecedesStart(t *testing.T) {
	started := now
	finished := now.Add(-time.Minute)
	pod := runningPod("p1", started)
	pod.TerminatedAt = &finished

	plan := Reconcile([]k3s.PodSnapshot{pod}, nil, apps, now)
	if len(plan.Closes) != 1 || plan.Closes[0].EndedAt.Before(started) {
		t.Fatalf("close clamped incorrectly: %+v", plan.Closes)
	}
}
