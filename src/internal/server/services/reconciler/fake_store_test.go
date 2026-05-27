package reconciler

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

// fakeStore is an in-memory Store used across reconciler unit tests.
// It is intentionally simple — no advanced indexing — because the
// tests exercise small fixtures.
type fakeStore struct {
	mu sync.Mutex

	jobs           []db.ReconciliationJobRow
	stuckBuilding  []db.StuckDeployRun
	stuckSyncing   []db.StuckDeployRun
	transitions    []db.DeployRunTransitionParams
	transitionErr  map[string]error
	deployStatuses map[string]string

	incidents      []db.IncidentRow
	nextIncidentID int
	clock          func() time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		transitionErr:  make(map[string]error),
		deployStatuses: make(map[string]string),
		clock:          time.Now,
	}
}

func (s *fakeStore) ListPendingReconciliationJobs(_ context.Context, now time.Time, limit int) ([]db.ReconciliationJobRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]db.ReconciliationJobRow, 0, len(s.jobs))
	for _, j := range s.jobs {
		if j.Status != "pending" {
			continue
		}
		if j.NextAttemptAt != nil && j.NextAttemptAt.After(now) {
			continue
		}
		out = append(out, j)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *fakeStore) CountPendingReconciliationJobsByKind(_ context.Context) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int)
	for _, j := range s.jobs {
		if j.Status == "pending" {
			out[j.Kind]++
		}
	}
	return out, nil
}

func (s *fakeStore) ClaimReconciliationJob(_ context.Context, jobID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.ID == jobID && j.Status == "pending" {
			s.jobs[i].Status = "in_progress"
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) CompleteReconciliationJob(_ context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock().UTC()
	for i, j := range s.jobs {
		if j.ID == jobID && j.Status != "failed_permanently" {
			s.jobs[i].Status = "completed"
			s.jobs[i].CompletedAt = &now
			return nil
		}
	}
	return nil
}

func (s *fakeStore) RescheduleReconciliationJob(_ context.Context, jobID string, lastErr string, nextAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.ID == jobID {
			s.jobs[i].Attempts = j.Attempts + 1
			ts := nextAt.UTC()
			s.jobs[i].NextAttemptAt = &ts
			err := lastErr
			s.jobs[i].LastError = &err
			s.jobs[i].Status = "pending"
			return nil
		}
	}
	return errors.New("job not found")
}

func (s *fakeStore) FailReconciliationJobPermanently(_ context.Context, jobID, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock().UTC()
	for i, j := range s.jobs {
		if j.ID == jobID {
			s.jobs[i].Status = "failed_permanently"
			err := lastErr
			s.jobs[i].LastError = &err
			s.jobs[i].CompletedAt = &now
			return nil
		}
	}
	return errors.New("job not found")
}

func (s *fakeStore) ListStuckBuildingDeployRuns(_ context.Context, threshold time.Time, _ int) ([]db.StuckDeployRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]db.StuckDeployRun, 0, len(s.stuckBuilding))
	for _, r := range s.stuckBuilding {
		if r.Status != "" && r.Status != "building" {
			continue
		}
		if r.StartedAt == nil || r.StartedAt.After(threshold) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeStore) ListStuckSyncingDeployRuns(_ context.Context, threshold time.Time, _ int) ([]db.StuckDeployRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]db.StuckDeployRun, 0, len(s.stuckSyncing))
	for _, r := range s.stuckSyncing {
		if r.Status != "" && r.Status != "syncing" {
			continue
		}
		if r.StartedAt == nil || r.StartedAt.After(threshold) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeStore) TransitionDeployRun(_ context.Context, params db.DeployRunTransitionParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.transitionErr[params.RunID]; ok && err != nil {
		return err
	}
	if cur, ok := s.deployStatuses[params.RunID]; ok && cur != "" && cur != params.FromStatus {
		return db.ErrDeployRunStateConflict
	}
	s.transitions = append(s.transitions, params)
	s.deployStatuses[params.RunID] = params.ToStatus
	return nil
}

func (s *fakeStore) InsertIncident(_ context.Context, in db.IncidentInsert) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextIncidentID++
	id := "incident-" + intToStr(s.nextIncidentID)
	openedAt := s.clock().UTC()
	row := db.IncidentRow{
		ID:          id,
		TeamID:      in.TeamID,
		SubjectType: in.SubjectType,
		SubjectID:   in.SubjectID,
		Kind:        in.Kind,
		Severity:    in.Severity,
		OpenedAt:    openedAt,
		Description: in.Description,
		TraceID:     in.TraceID,
	}
	s.incidents = append(s.incidents, row)
	return id, openedAt, nil
}

func (s *fakeStore) GetIncident(_ context.Context, teamID, id string) (db.IncidentRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.incidents {
		if row.TeamID == teamID && row.ID == id {
			return row, nil
		}
	}
	return db.IncidentRow{}, db.ErrIncidentNotFound
}

func (s *fakeStore) ListIncidents(_ context.Context, filter db.IncidentListFilter) (db.IncidentListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]db.IncidentRow, 0, len(s.incidents))
	for _, row := range s.incidents {
		if row.TeamID != filter.TeamID {
			continue
		}
		switch filter.Status {
		case "open":
			if row.ClosedAt != nil {
				continue
			}
		case "closed":
			if row.ClosedAt == nil {
				continue
			}
		}
		if filter.Kind != "" && row.Kind != filter.Kind {
			continue
		}
		if filter.Severity != "" && row.Severity != filter.Severity {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].OpenedAt.After(out[j].OpenedAt)
	})
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	result := db.IncidentListResult{Items: out}
	if len(out) > pageSize {
		result.Items = out[:pageSize]
	}
	return result, nil
}

func (s *fakeStore) CloseIncident(_ context.Context, teamID, id, closedBy, note string) (db.IncidentRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, row := range s.incidents {
		if row.TeamID != teamID || row.ID != id {
			continue
		}
		if row.ClosedAt == nil {
			now := s.clock().UTC()
			s.incidents[i].ClosedAt = &now
			cb := closedBy
			s.incidents[i].ClosedBy = &cb
			if note != "" {
				n := note
				s.incidents[i].ClosedNote = &n
			}
		}
		return s.incidents[i], nil
	}
	return db.IncidentRow{}, db.ErrIncidentNotFound
}

func (s *fakeStore) CountOpenIncidents(_ context.Context) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int)
	for _, row := range s.incidents {
		if row.ClosedAt == nil {
			out[row.Severity]++
		}
	}
	return out, nil
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
