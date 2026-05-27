package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

type fakeAudit struct {
	entries []AuditEntry
	err     error
}

func (f *fakeAudit) Log(_ context.Context, e AuditEntry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

func TestIncidentServiceOpenAndList(t *testing.T) {
	store := newFakeStore()
	svc := NewIncidentService(store, nil, NopObserver())
	row, err := svc.Open(context.Background(), OpenParams{
		TeamID:      "team-1",
		SubjectType: "deploy_run",
		SubjectID:   "00000000-0000-0000-0000-000000000001",
		Kind:        IncidentKindFailedPermanently,
		Severity:    SeverityMedium,
		Description: "test incident",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if row.ID == "" || row.OpenedAt.IsZero() {
		t.Fatalf("row missing fields: %+v", row)
	}
	out, err := svc.List(context.Background(), db.IncidentListFilter{TeamID: "team-1", Status: "open"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != row.ID {
		t.Fatalf("List items = %+v", out.Items)
	}
}

func TestIncidentServiceCloseEmitsAudit(t *testing.T) {
	store := newFakeStore()
	audit := &fakeAudit{}
	svc := NewIncidentService(store, audit, NopObserver())
	row, _ := svc.Open(context.Background(), OpenParams{
		TeamID:      "team-1",
		SubjectType: "deploy_run",
		SubjectID:   "00000000-0000-0000-0000-000000000001",
		Kind:        IncidentKindFailedPermanently,
		Severity:    SeverityMedium,
	})
	closed, err := svc.Close(context.Background(), CloseParams{
		TeamID:     "team-1",
		IncidentID: row.ID,
		ActorID:    "user-1",
		Note:       "root cause: GitHub API outage",
	})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.ClosedAt == nil || closed.ClosedBy == nil || *closed.ClosedBy != "user-1" {
		t.Fatalf("close did not stamp metadata: %+v", closed)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(audit.entries))
	}
	entry := audit.entries[0]
	if entry.Action != "incident_close" || entry.Source != "user" || entry.SubjectType != "incident" {
		t.Fatalf("audit entry wrong fields: %+v", entry)
	}
}

func TestIncidentServiceCloseAlreadyClosedReturnsErr(t *testing.T) {
	store := newFakeStore()
	svc := NewIncidentService(store, nil, NopObserver())
	row, _ := svc.Open(context.Background(), OpenParams{
		TeamID:      "team-1",
		SubjectType: "deploy_run",
		SubjectID:   "00000000-0000-0000-0000-000000000001",
		Kind:        IncidentKindFailedPermanently,
	})
	if _, err := svc.Close(context.Background(), CloseParams{TeamID: "team-1", IncidentID: row.ID, ActorID: "user-1"}); err != nil {
		t.Fatalf("first close: %v", err)
	}
	_, err := svc.Close(context.Background(), CloseParams{TeamID: "team-1", IncidentID: row.ID, ActorID: "user-1"})
	if !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("expected ErrAlreadyClosed, got %v", err)
	}
}

func TestIncidentServiceCloseCrossTeamReturnsNotFound(t *testing.T) {
	store := newFakeStore()
	svc := NewIncidentService(store, nil, NopObserver())
	row, _ := svc.Open(context.Background(), OpenParams{
		TeamID:      "team-1",
		SubjectType: "deploy_run",
		SubjectID:   "00000000-0000-0000-0000-000000000001",
		Kind:        IncidentKindFailedPermanently,
	})
	_, err := svc.Close(context.Background(), CloseParams{TeamID: "team-2", IncidentID: row.ID, ActorID: "user-1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestIncidentServiceOpenRejectsEmptyInputs(t *testing.T) {
	store := newFakeStore()
	svc := NewIncidentService(store, nil, NopObserver())
	if _, err := svc.Open(context.Background(), OpenParams{Kind: IncidentKindFailedPermanently}); err == nil {
		t.Fatalf("expected error for empty team_id")
	}
	if _, err := svc.Open(context.Background(), OpenParams{TeamID: "t"}); err == nil {
		t.Fatalf("expected error for empty kind")
	}
}

func TestIncidentServiceClockOverride(t *testing.T) {
	store := newFakeStore()
	svc := NewIncidentService(store, nil, NopObserver())
	fixed := time.Date(2026, 5, 16, 8, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return fixed }
	svc.SetClock(func() time.Time { return fixed })
	row, _ := svc.Open(context.Background(), OpenParams{
		TeamID:      "team-1",
		SubjectType: "deploy_run",
		SubjectID:   "00000000-0000-0000-0000-000000000001",
		Kind:        IncidentKindFailedPermanently,
	})
	if !row.OpenedAt.Equal(fixed) {
		t.Fatalf("OpenedAt = %s, want %s", row.OpenedAt, fixed)
	}
}
