package db

import (
	"context"
	"errors"
	"testing"
)

type fakeRow struct {
	value string
	err   error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 0 {
		return errors.New("no destination")
	}
	target, ok := dest[0].(*string)
	if !ok {
		return errors.New("destination is not *string")
	}
	*target = r.value
	return nil
}

type fakeProbe struct {
	row *fakeRow
}

func (f *fakeProbe) QueryRow(ctx context.Context, sql string, args ...any) PrimaryQueryRow {
	return f.row
}

func TestEnsurePrimaryAcceptsOff(t *testing.T) {
	if err := EnsurePrimary(context.Background(), &fakeProbe{row: &fakeRow{value: "off"}}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestEnsurePrimaryRejectsOn(t *testing.T) {
	err := EnsurePrimary(context.Background(), &fakeProbe{row: &fakeRow{value: "on"}})
	if !errors.Is(err, ErrConnectedToReplica) {
		t.Errorf("want ErrConnectedToReplica, got %v", err)
	}
}

func TestEnsurePrimaryWrapsQueryError(t *testing.T) {
	wantErr := errors.New("connection refused")
	err := EnsurePrimary(context.Background(), &fakeProbe{row: &fakeRow{err: wantErr}})
	if err == nil || !errors.Is(err, wantErr) {
		t.Errorf("want wrapped %v, got %v", wantErr, err)
	}
}

func TestEnsurePrimaryRequiresProbe(t *testing.T) {
	if err := EnsurePrimary(context.Background(), nil); err == nil {
		t.Errorf("want error on nil probe, got nil")
	}
}
