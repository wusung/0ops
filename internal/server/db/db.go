// Package db provides the database pool and query helpers.
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/winshare/zeroops/internal/server/db/sqlc"
)

// TX matches the sqlc DB interface used by the repository.
type TX = sqlcgen.DBTX

// NewPool creates a pgx pool from the provided configuration.
func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	cfg = cfg.withDefaults()

	if cfg.URL == "" {
		return nil, errors.New("database url is required")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, err
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

// MustPing verifies that the pool can reach the database.
func MustPing(ctx context.Context, pool *pgxpool.Pool) error {
	return pool.Ping(ctx)
}

// NewQueries returns a sqlc query wrapper for db.
func NewQueries(db TX) *sqlcgen.Queries {
	return sqlcgen.New(db)
}
