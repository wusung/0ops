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
func ConfigFromEnv() Config {
	return Config{
		URL:             os.Getenv("DATABASE_URL"),
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
