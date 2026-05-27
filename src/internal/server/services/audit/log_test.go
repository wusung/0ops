package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeWriter struct {
	rows []InsertRow
	err  error
}

func (f *fakeWriter) InsertAuditLog(_ context.Context, row InsertRow) error {
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, row)
	return nil
}

type fakeReader struct{}

func (fakeReader) ListAuditLog(context.Context, QueryFilter) (QueryResult, error) {
	return QueryResult{}, nil
}
func (fakeReader) GetAuditLogByID(context.Context, string, int64) (Row, error) {
	return Row{}, ErrNotFound
}

type captureObserver struct {
	writes []string
}

func (c *captureObserver) ObserveWrite(_ string, outcome string) {
	c.writes = append(c.writes, outcome)
}
func (c *captureObserver) ObserveQuery(string, string)     {}
func (c *captureObserver) ObservePartitionRollover(string) {}

func TestLogRedactsArgsAndResult(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	obs := &captureObserver{}
	svc := NewService(w, fakeReader{}, obs)

	actor := "00000000-0000-0000-0000-000000000001"
	err := svc.Log(context.Background(), Entry{
		TeamID:      "11111111-1111-1111-1111-111111111111",
		ActorUserID: &actor,
		Action:      "token_create",
		Args:        map[string]any{"name": "ci", "token": "op_pat_xxx"},
		Result:      map[string]any{"id": "tok-1", "secret_value": "raw"},
		TraceID:     "trace-1",
	})
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	if len(w.rows) != 1 {
		t.Fatalf("expected 1 row written, got %d", len(w.rows))
	}

	got := w.rows[0]
	var args, result map[string]any
	if err := json.Unmarshal(got.Args, &args); err != nil {
		t.Fatalf("args unmarshal: %v", err)
	}
	if err := json.Unmarshal(got.Result, &result); err != nil {
		t.Fatalf("result unmarshal: %v", err)
	}
	if args["token"] != "***" {
		t.Fatalf("token not redacted in args: %#v", args)
	}
	if args["name"] != "ci" {
		t.Fatalf("name was modified: %#v", args)
	}
	if result["secret_value"] != "***" {
		t.Fatalf("secret_value not redacted in result: %#v", result)
	}
	if got.Source != string(SourceUser) || got.Outcome != string(OutcomeSuccess) {
		t.Fatalf("defaults not applied: source=%q outcome=%q", got.Source, got.Outcome)
	}
	if got.TraceID != "trace-1" {
		t.Fatalf("trace_id not preserved: %q", got.TraceID)
	}
	if len(obs.writes) != 1 || obs.writes[0] != string(OutcomeSuccess) {
		t.Fatalf("metric observer not invoked: %#v", obs.writes)
	}
}

func TestLogFillsTraceIDSentinelWhenMissing(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	svc := NewService(w, fakeReader{}, nil)

	err := svc.Log(context.Background(), Entry{
		TeamID: "11111111-1111-1111-1111-111111111111",
		Action: "system_action",
		Source: SourceSystem,
	})
	if err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	if w.rows[0].TraceID != "00000000000000000000000000000000" {
		t.Fatalf("expected 32-zero trace sentinel, got %q", w.rows[0].TraceID)
	}
}

func TestLogRejectsNonUserSourceWithActor(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{}
	svc := NewService(w, fakeReader{}, nil)
	actor := "00000000-0000-0000-0000-000000000001"
	err := svc.Log(context.Background(), Entry{
		TeamID:      "team",
		Action:      "webhook_received",
		Source:      SourceWebhook,
		ActorUserID: &actor,
	})
	if err == nil || !strings.Contains(err.Error(), "actor_user_id") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if len(w.rows) != 0 {
		t.Fatalf("validation error must not insert: %#v", w.rows)
	}
}

func TestLogPropagatesWriterError(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{err: errors.New("boom")}
	obs := &captureObserver{}
	svc := NewService(w, fakeReader{}, obs)
	err := svc.Log(context.Background(), Entry{
		TeamID:  "11111111-1111-1111-1111-111111111111",
		Action:  "x",
		TraceID: "t",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if len(obs.writes) != 1 || obs.writes[0] != "write_error" {
		t.Fatalf("observer outcome not write_error: %#v", obs.writes)
	}
}

func TestTraceIDFromContextHonoursOverride(t *testing.T) {
	t.Parallel()
	ctx := WithTraceID(context.Background(), "abc")
	if TraceIDFromContext(ctx) != "abc" {
		t.Fatalf("override not honoured")
	}
	if TraceIDFromContext(context.Background()) != "00000000000000000000000000000000" {
		t.Fatalf("missing trace should fall back to sentinel")
	}
}
