package db_test

import (
	"testing"

	dbpkg "github.com/winshare/zeroops/internal/server/db"
)

// TestConfigFromEnvPrefersAppDatabaseURL pins the slice-b2 connection switch
// (audit-export-and-integrity spec § 5.1): the runtime connects under the
// append-only "0ops_app" envelope via APP_DATABASE_URL, while migrate / ops
// tooling keeps using DATABASE_URL. APP_DATABASE_URL therefore wins when set,
// and DATABASE_URL remains the fallback for backward compatibility.
func TestConfigFromEnvPrefersAppDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable")
	t.Setenv("APP_DATABASE_URL", "postgres://app:app_pw@db:5432/ops?sslmode=disable")

	cfg := dbpkg.ConfigFromEnv()

	want := "postgres://app:app_pw@db:5432/ops?sslmode=disable" //nolint:gosec // test fixture DSN
	if cfg.URL != want {
		t.Fatalf("expected APP_DATABASE_URL to win, got %q", cfg.URL)
	}
}

// TestConfigFromEnvFallsBackToDatabaseURL confirms the fallback when no
// APP_DATABASE_URL is provided (e.g. a dev box that has not provisioned the
// restricted login role yet).
func TestConfigFromEnvFallsBackToDatabaseURL(t *testing.T) {
	t.Setenv("APP_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable")

	cfg := dbpkg.ConfigFromEnv()

	want := "postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable" //nolint:gosec // test fixture DSN
	if cfg.URL != want {
		t.Fatalf("expected DATABASE_URL fallback, got %q", cfg.URL)
	}
}
