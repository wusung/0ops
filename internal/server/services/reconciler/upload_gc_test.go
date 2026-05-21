package reconciler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

// --- fake implementations ---

type fakeUploadGCStore struct {
	listRows  []db.Upload
	listErr   error
	markErr   error
	listCalls int
	listLimit int
	markCalls []string
}

func (f *fakeUploadGCStore) ListExpiredUploads(_ context.Context, limit int) ([]db.Upload, error) {
	f.listCalls++
	f.listLimit = limit
	return f.listRows, f.listErr
}

func (f *fakeUploadGCStore) MarkUploadGCd(_ context.Context, id string) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.markCalls = append(f.markCalls, id)
	return nil
}

type fakeUploadGCIngest struct {
	deleteCalls []string
	deleteErr   error
	failFirst   bool
}

func (f *fakeUploadGCIngest) Delete(_ context.Context, team, id string) error {
	f.deleteCalls = append(f.deleteCalls, team+"/"+id)
	if f.failFirst && len(f.deleteCalls) == 1 {
		return errors.New("simulated disk error")
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return nil
}

type fakeUploadGCAudit struct {
	entries []audit.Entry
	err     error
}

func (f *fakeUploadGCAudit) Log(_ context.Context, e audit.Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

// --- helpers ---

func makeUpload(id, teamID, status string) db.Upload {
	return db.Upload{
		ID:        id,
		TeamID:    teamID,
		Status:    status,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}
}

// --- tests ---

func TestUploadGC_Tick_DeletesExpiredAndMarksGCd(t *testing.T) {
	store := &fakeUploadGCStore{
		listRows: []db.Upload{
			makeUpload("upload-1", "team-a", "received"),
			makeUpload("upload-2", "team-a", "pinned"),
		},
	}
	ingest := &fakeUploadGCIngest{}
	auditW := &fakeUploadGCAudit{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW}
	processed, failed := s.Tick(context.Background())

	if processed != 2 {
		t.Errorf("processed = %d, want 2", processed)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	if len(ingest.deleteCalls) != 2 {
		t.Errorf("delete calls = %d, want 2", len(ingest.deleteCalls))
	}
	if len(store.markCalls) != 2 {
		t.Errorf("mark calls = %d, want 2", len(store.markCalls))
	}
	if len(auditW.entries) != 2 {
		t.Errorf("audit entries = %d, want 2", len(auditW.entries))
	}
}

func TestUploadGC_Tick_IngestDeleteFailureIsolated(t *testing.T) {
	store := &fakeUploadGCStore{
		listRows: []db.Upload{
			makeUpload("upload-1", "team-a", "received"),
			makeUpload("upload-2", "team-a", "received"),
		},
	}
	ingest := &fakeUploadGCIngest{failFirst: true}
	auditW := &fakeUploadGCAudit{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW}
	processed, failed := s.Tick(context.Background())

	if processed != 1 {
		t.Errorf("processed = %d, want 1", processed)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	// Second row should still have been attempted.
	if len(ingest.deleteCalls) != 2 {
		t.Errorf("delete calls = %d, want 2 (both attempted)", len(ingest.deleteCalls))
	}
	// Second row's mark should be called (only one succeeded).
	if len(store.markCalls) != 1 {
		t.Errorf("mark calls = %d, want 1", len(store.markCalls))
	}
	if store.markCalls[0] != "upload-2" {
		t.Errorf("mark call ID = %q, want upload-2", store.markCalls[0])
	}
}

func TestUploadGC_Tick_MarkGCdFailureIsolated(t *testing.T) {
	store := &fakeUploadGCStore{
		listRows: []db.Upload{makeUpload("upload-1", "team-a", "received")},
		markErr:  errors.New("db error"),
	}
	ingest := &fakeUploadGCIngest{}
	auditW := &fakeUploadGCAudit{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW}
	processed, failed := s.Tick(context.Background())

	if processed != 0 {
		t.Errorf("processed = %d, want 0", processed)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	// Audit must NOT be called when mark failed.
	if len(auditW.entries) != 0 {
		t.Errorf("audit entries = %d, want 0 (no audit after mark failure)", len(auditW.entries))
	}
}

func TestUploadGC_Tick_AuditFailureNotFatal(t *testing.T) {
	store := &fakeUploadGCStore{
		listRows: []db.Upload{makeUpload("upload-1", "team-a", "received")},
	}
	ingest := &fakeUploadGCIngest{}
	auditW := &fakeUploadGCAudit{err: errors.New("audit db down")}

	var warnLogged bool
	log := slog.New(slog.NewTextHandler(warnWriter{fn: func(s string) {
		if strings.Contains(s, "audit log failed") {
			warnLogged = true
		}
	}}, &slog.HandlerOptions{Level: slog.LevelWarn}))

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW, Logger: log}
	processed, failed := s.Tick(context.Background())

	if processed != 1 {
		t.Errorf("processed = %d, want 1 (audit failure must not count as failure)", processed)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	if !warnLogged {
		t.Error("expected Warn log for audit failure, got none")
	}
}

func TestUploadGC_Tick_ListFailureLogsAndReturns(t *testing.T) {
	store := &fakeUploadGCStore{listErr: errors.New("db timeout")}
	ingest := &fakeUploadGCIngest{}
	auditW := &fakeUploadGCAudit{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW}
	processed, failed := s.Tick(context.Background())

	if processed != 0 || failed != 0 {
		t.Errorf("processed=%d failed=%d, want 0,0", processed, failed)
	}
	if len(ingest.deleteCalls) != 0 {
		t.Error("delete should not be called when list fails")
	}
	if len(store.markCalls) != 0 {
		t.Error("mark should not be called when list fails")
	}
	if len(auditW.entries) != 0 {
		t.Error("audit should not be called when list fails")
	}
}

func TestUploadGC_Tick_EmptyList(t *testing.T) {
	store := &fakeUploadGCStore{listRows: []db.Upload{}}
	ingest := &fakeUploadGCIngest{}
	auditW := &fakeUploadGCAudit{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW}
	processed, failed := s.Tick(context.Background())

	if processed != 0 || failed != 0 {
		t.Errorf("processed=%d failed=%d, want 0,0", processed, failed)
	}
	if len(ingest.deleteCalls) != 0 || len(store.markCalls) != 0 || len(auditW.entries) != 0 {
		t.Error("no calls expected for empty list")
	}
}

func TestUploadGC_Tick_ContextCancelledMidLoop(t *testing.T) {
	store := &fakeUploadGCStore{
		listRows: []db.Upload{
			makeUpload("upload-1", "team-a", "received"),
			makeUpload("upload-2", "team-a", "received"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	ingest := &cancelOnFirstIngest{
		cancel: cancel,
		onCall: func() { callCount++ },
	}
	auditW := &fakeUploadGCAudit{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: auditW}
	processed, failed := s.Tick(ctx)

	// First row processed, second skipped due to ctx cancel.
	if processed != 1 {
		t.Errorf("processed = %d, want 1", processed)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	if callCount != 1 {
		t.Errorf("ingest delete called %d times, want 1", callCount)
	}
}

// cancelOnFirstIngest cancels ctx after the first Delete call succeeds,
// simulating context cancellation mid-batch.
type cancelOnFirstIngest struct {
	cancel  context.CancelFunc
	onCall  func()
	calls   int
}

func (c *cancelOnFirstIngest) Delete(_ context.Context, _, _ string) error {
	c.calls++
	c.onCall()
	// Cancel after first successful delete — next iteration will see ctx.Err().
	c.cancel()
	return nil
}

func TestUploadGC_Tick_NilScannerNoop(t *testing.T) {
	var s *UploadGCScanner
	// Must not panic.
	processed, failed := s.Tick(context.Background())
	if processed != 0 || failed != 0 {
		t.Errorf("nil scanner: processed=%d failed=%d, want 0,0", processed, failed)
	}
}

func TestUploadGC_Tick_LimitDefault(t *testing.T) {
	store := &fakeUploadGCStore{listRows: []db.Upload{}}
	ingest := &fakeUploadGCIngest{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Limit: 0}
	s.Tick(context.Background())

	if store.listLimit != defaultUploadGCBatch {
		t.Errorf("list limit = %d, want %d (defaultUploadGCBatch)", store.listLimit, defaultUploadGCBatch)
	}
}

func TestUploadGC_Tick_LimitRespected(t *testing.T) {
	store := &fakeUploadGCStore{listRows: []db.Upload{}}
	ingest := &fakeUploadGCIngest{}

	s := &UploadGCScanner{Store: store, Ingest: ingest, Limit: 5}
	s.Tick(context.Background())

	if store.listLimit != 5 {
		t.Errorf("list limit = %d, want 5", store.listLimit)
	}
}

func TestUploadGC_AuditEntryShape(t *testing.T) {
	exp := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	row := db.Upload{
		ID:            "upload-abc",
		TeamID:        "team-xyz",
		Status:        "pinned",
		ExpiresAt:     exp,
		SizeBytes:     1234,
		ArchiveFormat: "tar.zst",
	}
	entry := makeUploadGCAudit(row)

	if entry.Action != "app_source.upload.gc_d" {
		t.Errorf("Action = %q, want app_source.upload.gc_d", entry.Action)
	}
	if entry.SubjectType != "upload" {
		t.Errorf("SubjectType = %q, want upload", entry.SubjectType)
	}
	if entry.SubjectID == nil || *entry.SubjectID != "upload-abc" {
		t.Errorf("SubjectID = %v, want &upload-abc", entry.SubjectID)
	}
	if entry.Source != audit.SourceSystem {
		t.Errorf("Source = %q, want system", entry.Source)
	}
	if entry.ActorUserID != nil {
		t.Errorf("ActorUserID = %v, want nil (system source requires nil actor)", entry.ActorUserID)
	}
	if entry.Outcome != audit.OutcomeSuccess {
		t.Errorf("Outcome = %q, want success", entry.Outcome)
	}
	if entry.TeamID != "team-xyz" {
		t.Errorf("TeamID = %q, want team-xyz", entry.TeamID)
	}
	result, ok := entry.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result is not map[string]any: %T", entry.Result)
	}
	if result["prior_status"] != "pinned" {
		t.Errorf("prior_status = %v, want pinned", result["prior_status"])
	}
	if result["expires_at"] != exp {
		t.Errorf("expires_at = %v, want %v", result["expires_at"], exp)
	}
}

func TestUploadGC_NilAuditSkipsAudit(t *testing.T) {
	store := &fakeUploadGCStore{
		listRows: []db.Upload{makeUpload("upload-1", "team-a", "received")},
	}
	ingest := &fakeUploadGCIngest{}

	// Audit = nil — must not panic and row must count as processed.
	s := &UploadGCScanner{Store: store, Ingest: ingest, Audit: nil}
	processed, failed := s.Tick(context.Background())

	if processed != 1 {
		t.Errorf("processed = %d, want 1", processed)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
}

// warnWriter captures log output for inspection in tests.
type warnWriter struct {
	fn func(string)
}

func (w warnWriter) Write(p []byte) (int, error) {
	w.fn(string(p))
	return len(p), nil
}
