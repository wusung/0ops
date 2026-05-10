package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/winshare/zeroops/internal/server/db/sqlc"
)

type DBTX = sqlcgen.DBTX

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

func MustPing(ctx context.Context, pool *pgxpool.Pool) error {
	return pool.Ping(ctx)
}

func NewQueries(db DBTX) *sqlcgen.Queries {
	return sqlcgen.New(db)
}
