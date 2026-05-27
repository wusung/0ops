package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrConnectedToReplica is returned by EnsurePrimary when the pool resolves
// to a read-only standby. Spec § 4.2 reserves the writeable path for the
// postgres-main Service; backend pods that boot against a replica DSN must
// refuse to serve traffic instead of issuing writes that silently fail at
// statement time.
var ErrConnectedToReplica = errors.New("connected to read-only replica; backend requires the writeable primary")

// Probe is the minimal interface EnsurePrimary needs from a pool. It is
// satisfied by *pgxpool.Pool and by the test fake.
type Probe interface {
	QueryRow(ctx context.Context, sql string, args ...any) PrimaryQueryRow
}

// PrimaryQueryRow is the row-shaped contract returned by Probe.QueryRow.
// Returning a custom interface keeps the package testable without
// importing the heavy pgx Row type.
type PrimaryQueryRow interface {
	Scan(dest ...any) error
}

// EnsurePrimary issues `SHOW transaction_read_only` and returns
// ErrConnectedToReplica when the value is "on". Other errors are wrapped
// and returned as-is.
//
// Callers (cmd/server) run this once on startup, after MustPing, so that
// a misrouted DATABASE_URL Secret is caught before any write handler
// receives traffic — spec § 16 hard rule #10 + ADR-0008 §4 (7).
func EnsurePrimary(ctx context.Context, p Probe) error {
	if p == nil {
		return errors.New("EnsurePrimary requires non-nil probe")
	}
	row := p.QueryRow(ctx, "SHOW transaction_read_only")
	var value string
	if err := row.Scan(&value); err != nil {
		return fmt.Errorf("query transaction_read_only: %w", err)
	}
	if value == "on" {
		return ErrConnectedToReplica
	}
	return nil
}

// PoolProbe wraps a *pgxpool.Pool so it can be passed to EnsurePrimary.
type PoolProbe struct{ Pool *pgxpool.Pool }

// QueryRow satisfies the Probe interface against the pgx pool.
func (p PoolProbe) QueryRow(ctx context.Context, sql string, args ...any) PrimaryQueryRow {
	return p.Pool.QueryRow(ctx, sql, args...)
}
