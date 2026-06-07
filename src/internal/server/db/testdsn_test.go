package db_test

import (
	"os"
	"strings"
	"testing"
)

// resolveTestDatabaseURL picks the DSN for host-side DB tests.
//
// Resolution order:
//  1. $TEST_DATABASE_URL (explicit override; bypasses translation)
//  2. $DATABASE_URL with compose-internal hostname translated to
//     127.0.0.1:15432 (host-mapped port from compose.override.yaml).
//     This lets `bash manage.sh test` work from the host without
//     requiring users to keep dual DSN env vars.
//  3. Empty string → caller t.Skip.
//
// In-container tests (running under `podman compose exec`) keep
// $DATABASE_URL pointed at @db:5432 and resolve to themselves
// when TEST_DATABASE_URL is set to the same value or unset.
func resolveTestDatabaseURL() string {
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		return ""
	}
	return strings.ReplaceAll(u, "@db:5432", "@127.0.0.1:15432")
}

// TestResolveTestDatabaseURLTranslation verifies the host-side translation
// without needing a real DB.
func TestResolveTestDatabaseURLTranslation(t *testing.T) {
	cases := []struct {
		name     string
		testURL  string
		regular  string
		want     string
	}{
		{
			name:     "TEST_DATABASE_URL wins",
			testURL:  "postgres://test@127.0.0.1:15432/ops",
			regular:  "postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable",
			want:     "postgres://test@127.0.0.1:15432/ops",
		},
		{
			name:    "DATABASE_URL @db:5432 → @127.0.0.1:15432",
			regular: "postgres://ops:ops_dev_pw@db:5432/ops?sslmode=disable",
			want:    "postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable",
		},
		{
			name:    "DATABASE_URL already host-side stays put",
			regular: "postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable",
			want:    "postgres://ops:ops_dev_pw@127.0.0.1:15432/ops?sslmode=disable",
		},
		{
			name: "both empty → empty",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TEST_DATABASE_URL", c.testURL)
			t.Setenv("DATABASE_URL", c.regular)
			if got := resolveTestDatabaseURL(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
