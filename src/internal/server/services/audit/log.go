package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
)

// Source enumerates the origin of an audit_log row (spec § 4.1).
type Source string

const (
	// SourceUser is the default — a human-initiated action via CLI / MCP.
	SourceUser Source = "user"
	// SourceWebhook tags rows whose actor is a GitHub webhook.
	SourceWebhook Source = "webhook"
	// SourceReconciler tags rows whose actor is the reconciler.
	SourceReconciler Source = "reconciler"
	// SourceSystem tags rows whose actor is the backend itself.
	SourceSystem Source = "system"
)

// Outcome enumerates the result column of an audit_log row.
type Outcome string

const (
	// OutcomeSuccess marks a successful action.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure marks a failed action.
	OutcomeFailure Outcome = "failure"
	// OutcomeIdempotentReplay marks a replay of a previously successful action.
	OutcomeIdempotentReplay Outcome = "idempotent_replay"
)

// Entry is the input to Log(). args / result are scrubbed by the
// redactor before they hit the database (spec § 5.3, § 15 hard rule #2).
type Entry struct {
	TeamID      string
	ActorUserID *string
	Source      Source
	SubjectType string
	SubjectID   *string
	Action      string
	Args        any
	Result      any
	PreviewID   *string
	TraceID     string
	Outcome     Outcome
	HTTPStatus  *int
}

// Writer is the persistence boundary used by Service.Log. The
// production implementation is the audit Repository; tests substitute
// an in-memory fake.
type Writer interface {
	InsertAuditLog(ctx context.Context, row InsertRow) error
}

// InsertRow is the wire shape sent to the writer. Args / result are
// already serialised JSON so the writer can treat them opaquely.
type InsertRow struct {
	TeamID      string
	ActorUserID *string
	Source      string
	SubjectType string
	SubjectID   *string
	Action      string
	Args        json.RawMessage
	Result      json.RawMessage
	PreviewID   *string
	TraceID     string
	Outcome     string
	HTTPStatus  *int
}

// Service owns the Log + Query API for audit_log.
type Service struct {
	writer   Writer
	reader   Reader
	observer MetricObserver
}

// NewService wires the service. observer may be nil; callers that pass
// nil receive a NopObserver.
func NewService(writer Writer, reader Reader, observer MetricObserver) *Service {
	if observer == nil {
		observer = NopObserver()
	}
	return &Service{writer: writer, reader: reader, observer: observer}
}

// TraceIDFromContext returns the W3C-shaped trace id carried in ctx,
// falling back to the chi request id if no explicit trace id was set.
// Empty strings are normalised to the spec § 15 hard rule #3 sentinel
// of 32 zeroes so audit rows never lose the trace link.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return missingTraceSentinel
	}
	if id, ok := ctx.Value(traceIDContextKey).(string); ok && strings.TrimSpace(id) != "" {
		return id
	}
	if id := strings.TrimSpace(middleware.GetReqID(ctx)); id != "" {
		return id
	}
	return missingTraceSentinel
}

// WithTraceID returns a derived context whose audit trace id is fixed
// to the supplied value. Production code uses request middleware; tests
// use this to lock in a deterministic trace for assertions.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(traceID) == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDContextKey, traceID)
}

type traceIDContextKeyType struct{}

var traceIDContextKey = traceIDContextKeyType{}

// missingTraceSentinel is the spec § 15 hard rule #3 32-zero string
// that the audit_log records when no trace id propagated through.
const missingTraceSentinel = "00000000000000000000000000000000"

// Log writes one audit_log row. args / result are redacted in place
// so callers cannot accidentally smuggle secrets into the ledger.
// The trace_id falls back to the chi request id, and finally to the
// 32-zero sentinel — the row is always written (spec § 15 hard rule
// #10: audit_log writes must never silently fail; callers should
// surface the error so the reconciliation job can retry).
func (s *Service) Log(ctx context.Context, entry Entry) error {
	if s == nil {
		return errors.New("audit: nil service")
	}
	row, err := s.prepareInsert(ctx, entry)
	if err != nil {
		s.observer.ObserveWrite(entry.Action, "validation_error")
		return err
	}
	if err := s.writer.InsertAuditLog(ctx, row); err != nil {
		s.observer.ObserveWrite(row.Action, "write_error")
		return fmt.Errorf("audit: insert: %w", err)
	}
	s.observer.ObserveWrite(row.Action, row.Outcome)
	return nil
}

func (s *Service) prepareInsert(ctx context.Context, entry Entry) (InsertRow, error) {
	if strings.TrimSpace(entry.TeamID) == "" {
		return InsertRow{}, errors.New("audit: team_id is required")
	}
	if strings.TrimSpace(entry.Action) == "" {
		return InsertRow{}, errors.New("audit: action is required")
	}
	source := entry.Source
	if source == "" {
		source = SourceUser
	}
	if !validSource(source) {
		return InsertRow{}, fmt.Errorf("audit: invalid source %q", source)
	}
	if source != SourceUser && entry.ActorUserID != nil {
		return InsertRow{}, fmt.Errorf("audit: source %q requires actor_user_id to be NULL", source)
	}
	outcome := entry.Outcome
	if outcome == "" {
		outcome = OutcomeSuccess
	}
	if !validOutcome(outcome) {
		return InsertRow{}, fmt.Errorf("audit: invalid outcome %q", outcome)
	}

	argsJSON, err := encodeJSON(Redact(entry.Args))
	if err != nil {
		return InsertRow{}, fmt.Errorf("audit: encode args: %w", err)
	}
	resultJSON, err := encodeJSON(Redact(entry.Result))
	if err != nil {
		return InsertRow{}, fmt.Errorf("audit: encode result: %w", err)
	}

	traceID := strings.TrimSpace(entry.TraceID)
	if traceID == "" {
		traceID = TraceIDFromContext(ctx)
	}

	return InsertRow{
		TeamID:      entry.TeamID,
		ActorUserID: entry.ActorUserID,
		Source:      string(source),
		SubjectType: entry.SubjectType,
		SubjectID:   entry.SubjectID,
		Action:      entry.Action,
		Args:        argsJSON,
		Result:      resultJSON,
		PreviewID:   entry.PreviewID,
		TraceID:     traceID,
		Outcome:     string(outcome),
		HTTPStatus:  entry.HTTPStatus,
	}, nil
}

func validSource(s Source) bool {
	switch s {
	case SourceUser, SourceWebhook, SourceReconciler, SourceSystem:
		return true
	default:
		return false
	}
}

func validOutcome(o Outcome) bool {
	switch o {
	case OutcomeSuccess, OutcomeFailure, OutcomeIdempotentReplay:
		return true
	default:
		return false
	}
}

func encodeJSON(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}
