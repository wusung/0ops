package reconciler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// IncidentKind enumerates the auto-opened incident categories. New
// values MUST be reflected in spec § 9.2 and in the dashboard panel
// list before they ship.
type IncidentKind string

const (
	IncidentKindFailedPermanently   IncidentKind = "failed_permanently"
	IncidentKindUnknownSpike        IncidentKind = "unknown_failure_spike"
	IncidentKindCleanupResidueStuck IncidentKind = "cleanup_residue_stuck"
	IncidentKindGhcrRefreshFailed   IncidentKind = "ghcr_refresh_failed"
)

// IncidentSeverity is the fixed severity column. Spec § 9.2 captures
// the auto-mapping; ops may override at close time via CLI note.
type IncidentSeverity string

const (
	SeverityLow      IncidentSeverity = "low"
	SeverityMedium   IncidentSeverity = "medium"
	SeverityHigh     IncidentSeverity = "high"
	SeverityCritical IncidentSeverity = "critical"
)

// IncidentService bridges the reconciler core and the persistence
// layer: opening, listing, and closing incident rows. The HTTP / CLI
// handlers consume the same service so audit + metric emission stay
// centralised (spec § 10).
type IncidentService struct {
	store    Store
	clock    func() time.Time
	observer Observer
	audit    AuditWriter
}

// AuditWriter is the contract the incident service uses to record an
// audit_log row when an incident is closed. The interface mirrors the
// shape of audit.Service.Log so cmd/server can wire the real audit
// pipeline; tests substitute a fake.
type AuditWriter interface {
	Log(ctx context.Context, entry AuditEntry) error
}

// AuditEntry is the structural subset of audit.Entry that the
// reconciler uses. It exists to avoid an audit → reconciler dependency
// cycle; cmd/server adapts audit.Service to this interface.
type AuditEntry struct {
	TeamID      string
	ActorUserID *string
	Source      string
	SubjectType string
	SubjectID   *string
	Action      string
	Args        any
	Result      any
	TraceID     string
	Outcome     string
}

// NewIncidentService wires the service. observer may be nil; callers
// that pass nil receive a NopObserver.
func NewIncidentService(store Store, audit AuditWriter, observer Observer) *IncidentService {
	if observer == nil {
		observer = NopObserver()
	}
	return &IncidentService{
		store:    store,
		clock:    time.Now,
		observer: observer,
		audit:    audit,
	}
}

// SetClock overrides the time source for deterministic tests.
func (s *IncidentService) SetClock(now func() time.Time) {
	if now != nil {
		s.clock = now
	}
}

// ErrNotFound is the canonical "incident does not exist for this team"
// error. Cross-team queries collapse to ErrNotFound to avoid leaking
// existence (spec § 9.1 — same enumeration posture as audit / app).
var ErrNotFound = errors.New("reconciler: incident not found")

// ErrAlreadyClosed surfaces a no-op close attempt. The CLI translates
// it into a friendly "already closed" message.
var ErrAlreadyClosed = errors.New("reconciler: incident already closed")

// OpenParams captures the auto-incident inputs. Callers should NOT
// fill ClosedBy / ClosedNote — those belong to the close flow.
type OpenParams struct {
	TeamID      string
	SubjectType string
	SubjectID   string
	Kind        IncidentKind
	Severity    IncidentSeverity
	Description string
	TraceID     string
}

// Open inserts an incident row and emits the opened-counter metric.
// The reconciler runner calls this from failed_permanently transitions
// and from the unknown-spike detector. Audit log is intentionally NOT
// written here — spec § 10 says only auto-create writes audit on the
// failed_permanently job, which the runner handles separately.
func (s *IncidentService) Open(ctx context.Context, in OpenParams) (db.IncidentRow, error) {
	if strings.TrimSpace(in.TeamID) == "" {
		return db.IncidentRow{}, errors.New("reconciler: team_id is required")
	}
	if strings.TrimSpace(string(in.Kind)) == "" {
		return db.IncidentRow{}, errors.New("reconciler: kind is required")
	}
	severity := in.Severity
	if severity == "" {
		severity = SeverityMedium
	}
	description := strings.TrimSpace(in.Description)
	traceID := strings.TrimSpace(in.TraceID)
	row := db.IncidentInsert{
		TeamID:      in.TeamID,
		SubjectType: in.SubjectType,
		SubjectID:   in.SubjectID,
		Kind:        string(in.Kind),
		Severity:    string(severity),
	}
	if description != "" {
		row.Description = &description
	}
	if traceID != "" {
		row.TraceID = &traceID
	}
	id, openedAt, err := s.store.InsertIncident(ctx, row)
	if err != nil {
		return db.IncidentRow{}, fmt.Errorf("insert incident: %w", err)
	}
	s.observer.ObserveIncidentOpened(string(in.Kind), string(severity))
	out := db.IncidentRow{
		ID:          id,
		TeamID:      in.TeamID,
		SubjectType: in.SubjectType,
		SubjectID:   in.SubjectID,
		Kind:        string(in.Kind),
		Severity:    string(severity),
		OpenedAt:    openedAt,
	}
	if description != "" {
		out.Description = &description
	}
	if traceID != "" {
		out.TraceID = &traceID
	}
	return out, nil
}

// Get returns a single incident with team-scoped enforcement.
func (s *IncidentService) Get(ctx context.Context, teamID, id string) (db.IncidentRow, error) {
	row, err := s.store.GetIncident(ctx, teamID, id)
	if err != nil {
		if errors.Is(err, db.ErrIncidentNotFound) {
			return db.IncidentRow{}, ErrNotFound
		}
		return db.IncidentRow{}, fmt.Errorf("get incident: %w", err)
	}
	return row, nil
}

// List returns a keyset-paginated page.
func (s *IncidentService) List(ctx context.Context, filter db.IncidentListFilter) (db.IncidentListResult, error) {
	return s.store.ListIncidents(ctx, filter)
}

// CloseParams captures the wire inputs for the close action.
type CloseParams struct {
	TeamID    string
	IncidentID string
	ActorID   string
	Note      string
}

// Close marks an incident closed, writes an audit_log entry, and
// emits the closed-counter metric. Spec § 16 #8 mandates that the
// CLI is the only path to close — direct SQL is prohibited. Calling
// Close on an already-closed row returns ErrAlreadyClosed.
func (s *IncidentService) Close(ctx context.Context, in CloseParams) (db.IncidentRow, error) {
	existing, err := s.store.GetIncident(ctx, in.TeamID, in.IncidentID)
	if err != nil {
		if errors.Is(err, db.ErrIncidentNotFound) {
			return db.IncidentRow{}, ErrNotFound
		}
		return db.IncidentRow{}, fmt.Errorf("load incident: %w", err)
	}
	if existing.ClosedAt != nil {
		return db.IncidentRow{}, ErrAlreadyClosed
	}
	closed, err := s.store.CloseIncident(ctx, in.TeamID, in.IncidentID, in.ActorID, strings.TrimSpace(in.Note))
	if err != nil {
		if errors.Is(err, db.ErrIncidentNotFound) {
			return db.IncidentRow{}, ErrNotFound
		}
		return db.IncidentRow{}, fmt.Errorf("close incident: %w", err)
	}
	s.observer.ObserveIncidentClosed(closed.Kind, closed.Severity)
	if s.audit != nil {
		actor := in.ActorID
		subjectID := closed.ID
		traceID := ""
		if closed.TraceID != nil {
			traceID = *closed.TraceID
		}
		_ = s.audit.Log(ctx, AuditEntry{
			TeamID:      in.TeamID,
			ActorUserID: &actor,
			Source:      "user",
			SubjectType: "incident",
			SubjectID:   &subjectID,
			Action:      "incident_close",
			Args: map[string]any{
				"incident_id": closed.ID,
				"note":        strings.TrimSpace(in.Note),
			},
			Result: map[string]any{
				"kind":     closed.Kind,
				"severity": closed.Severity,
			},
			TraceID: traceID,
			Outcome: "success",
		})
	}
	return closed, nil
}
