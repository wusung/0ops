package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeExportReader satisfies both Reader and ExportReader; only the export
// surface is exercised here.
type fakeExportReader struct {
	rows      []ExportRow
	chains    []ChainHead
	gotFilter ExportFilter
}

func (f *fakeExportReader) ListAuditLog(context.Context, QueryFilter) (QueryResult, error) {
	return QueryResult{}, nil
}
func (f *fakeExportReader) GetAuditLogByID(context.Context, string, int64) (Row, error) {
	return Row{}, ErrNotFound
}
func (f *fakeExportReader) ExportAuditLog(_ context.Context, filter ExportFilter) ([]ExportRow, error) {
	f.gotFilter = filter
	n := len(f.rows)
	if filter.Limit > 0 && filter.Limit < n {
		n = filter.Limit
	}
	return f.rows[:n], nil
}
func (f *fakeExportReader) ListChainHeads(context.Context, string, time.Time, time.Time) ([]ChainHead, error) {
	return f.chains, nil
}

func newExportService(r *fakeExportReader) *Service {
	return NewService(nil, r, nil)
}

func TestExportRequiresSince(t *testing.T) {
	svc := newExportService(&fakeExportReader{})
	_, err := svc.Export(context.Background(), ExportRequest{TeamID: "t"})
	if !errors.Is(err, ErrExportSinceRequired) {
		t.Fatalf("want ErrExportSinceRequired, got %v", err)
	}
}

func TestExportRejectsRangeOver13Months(t *testing.T) {
	svc := newExportService(&fakeExportReader{})
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	until := since.AddDate(0, 14, 0)
	_, err := svc.Export(context.Background(), ExportRequest{TeamID: "t", Since: since, Until: until})
	if !errors.Is(err, ErrExportRangeTooLarge) {
		t.Fatalf("want ErrExportRangeTooLarge, got %v", err)
	}
}

func TestExportReturnsRowsChainsAndCursor(t *testing.T) {
	defer setExportMaxRowsForTest(t, 2)()

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	reader := &fakeExportReader{
		rows: []ExportRow{
			{Row: Row{ID: 1, CreatedAt: base}, RowHash: []byte{0x01}},
			{Row: Row{ID: 2, CreatedAt: base.Add(time.Second)}, RowHash: []byte{0x02}},
			{Row: Row{ID: 3, CreatedAt: base.Add(2 * time.Second)}, RowHash: []byte{0x03}},
		},
		chains: []ChainHead{{PartitionMonth: PartitionMonth(base), RowCount: 3}},
	}
	svc := newExportService(reader)

	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	res, err := svc.Export(context.Background(), ExportRequest{TeamID: "t", Since: since, Until: until})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Asks the reader for one beyond the cap so it can detect a further page.
	if reader.gotFilter.Limit != 3 {
		t.Fatalf("reader limit = %d, want cap+1 = 3", reader.gotFilter.Limit)
	}
	if reader.gotFilter.Since != since || reader.gotFilter.Until != until {
		t.Fatalf("reader range = [%v,%v], want [%v,%v]", reader.gotFilter.Since, reader.gotFilter.Until, since, until)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 (capped)", len(res.Rows))
	}
	if res.NextCursor == "" {
		t.Fatal("want NextCursor when more rows remain")
	}
	if len(res.Chains) != 1 || res.Chains[0].RowCount != 3 {
		t.Fatalf("chains not passed through: %+v", res.Chains)
	}

	// The cursor must resume right after the last returned row.
	ts, id, err := DecodeCursor(res.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if id != 2 || !ts.Equal(res.Rows[1].CreatedAt) {
		t.Fatalf("cursor = (%v,%d), want (%v,2)", ts, id, res.Rows[1].CreatedAt)
	}
}

func TestExportNoCursorWhenUnderCap(t *testing.T) {
	defer setExportMaxRowsForTest(t, 10)()

	reader := &fakeExportReader{
		rows: []ExportRow{{Row: Row{ID: 1, CreatedAt: time.Now().UTC()}}},
	}
	svc := newExportService(reader)
	since := time.Now().UTC().Add(-time.Hour)
	res, err := svc.Export(context.Background(), ExportRequest{TeamID: "t", Since: since})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.NextCursor != "" {
		t.Fatalf("want no cursor under cap, got %q", res.NextCursor)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
}

// setExportMaxRowsForTest temporarily lowers the page cap and returns a restore
// func, so the cursor/cap branch is exercised without materialising 100k rows.
func setExportMaxRowsForTest(t *testing.T, n int) func() {
	t.Helper()
	prev := exportMaxRows
	exportMaxRows = n
	return func() { exportMaxRows = prev }
}
