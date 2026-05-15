package audit

import (
	"context"
	"strings"
	"testing"
	"time"
)

type stubReader struct {
	captured QueryFilter
	rows     []Row
	getRow   Row
	getErr   error
}

func (s *stubReader) ListAuditLog(_ context.Context, f QueryFilter) (QueryResult, error) {
	s.captured = f
	return QueryResult{Items: s.rows}, nil
}

func (s *stubReader) GetAuditLogByID(context.Context, string, int64) (Row, error) {
	return s.getRow, s.getErr
}

func TestQueryAppliesDefaults(t *testing.T) {
	t.Parallel()
	reader := &stubReader{}
	svc := NewService(&fakeWriter{}, reader, nil)

	_, err := svc.Query(context.Background(), QueryFilter{TeamID: "team-1"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if reader.captured.PageSize != DefaultPageSize {
		t.Fatalf("expected default page size %d, got %d", DefaultPageSize, reader.captured.PageSize)
	}
	if reader.captured.Until.IsZero() {
		t.Fatalf("until should default to now")
	}
	if reader.captured.Since.IsZero() {
		t.Fatalf("since should default to 7d ago")
	}
	if reader.captured.Until.Sub(reader.captured.Since) != DefaultSinceWindow {
		t.Fatalf("default window mismatch: %v", reader.captured.Until.Sub(reader.captured.Since))
	}
	if reader.captured.Scope != QueryScopeTeam {
		t.Fatalf("expected default scope=team, got %q", reader.captured.Scope)
	}
}

func TestQueryClampsPageSize(t *testing.T) {
	t.Parallel()
	reader := &stubReader{}
	svc := NewService(&fakeWriter{}, reader, nil)
	_, err := svc.Query(context.Background(), QueryFilter{TeamID: "team", PageSize: 5000})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if reader.captured.PageSize != MaxPageSize {
		t.Fatalf("expected clamp to %d, got %d", MaxPageSize, reader.captured.PageSize)
	}
}

func TestQuerySelfScopeRequiresActor(t *testing.T) {
	t.Parallel()
	svc := NewService(&fakeWriter{}, &stubReader{}, nil)
	_, err := svc.Query(context.Background(), QueryFilter{TeamID: "team", Scope: QueryScopeSelf})
	if err == nil || !strings.Contains(err.Error(), "actor_user_id") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestGetRejectsCrossActor(t *testing.T) {
	t.Parallel()
	other := "00000000-0000-0000-0000-000000000099"
	reader := &stubReader{getRow: Row{ID: 7, ActorUserID: &other}}
	svc := NewService(&fakeWriter{}, reader, nil)
	_, err := svc.Get(context.Background(), "team", 7, QueryScopeSelf, "00000000-0000-0000-0000-000000000001")
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestEncodeDecodeCursorRoundtrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	enc := EncodeCursor(now, 42)
	gotTS, gotID, err := DecodeCursor(enc)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !gotTS.Equal(now) {
		t.Fatalf("ts roundtrip mismatch: got=%v want=%v", gotTS, now)
	}
	if gotID != 42 {
		t.Fatalf("id roundtrip mismatch: got=%d", gotID)
	}
}

func TestParsePageSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"", DefaultPageSize, false},
		{"50", 50, false},
		{"200", 200, false},
		{"201", 0, true},
		{"abc", 0, true},
		{"-1", 0, true},
	}
	for _, c := range cases {
		got, err := ParsePageSize(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("ParsePageSize(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParsePageSize(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParsePageSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
