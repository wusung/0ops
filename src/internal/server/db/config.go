// Package db provides database configuration helpers.
package db

import (
	"os"
	"time"
)

// Config holds database connection settings.
type Config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// ConfigFromEnv returns database settings loaded from environment defaults.
//
// The runtime connection prefers APP_DATABASE_URL — the append-only "0ops_app"
// envelope (audit-export-and-integrity spec § 5.1, migration 00014) — and falls
// back to DATABASE_URL when it is unset. migrate / ops tooling keeps using
// DATABASE_URL directly; only the long-running server picks up the restricted
// role, so a leaked runtime credential cannot UPDATE/DELETE audit_log.
func ConfigFromEnv() Config {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	return Config{
		URL:             url,
		MaxConns:        10,
		MinConns:        0,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
}

func (c Config) withDefaults() Config {
	defaults := ConfigFromEnv()

	if c.URL == "" {
		c.URL = defaults.URL
	}
	if c.MaxConns == 0 {
		c.MaxConns = defaults.MaxConns
	}
	if c.MinConns == 0 {
		c.MinConns = defaults.MinConns
	}
	if c.MaxConnLifetime == 0 {
		c.MaxConnLifetime = defaults.MaxConnLifetime
	}
	if c.MaxConnIdleTime == 0 {
		c.MaxConnIdleTime = defaults.MaxConnIdleTime
	}

	return c
}
