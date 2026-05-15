package migrationlint

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestRepoMigrationsPassLint scans the repository's migrations/ directory and
// fails the build if any migration at or above the grandfathered floor
// violates R1/R2.
//
// Pre-existing migrations (00001..0000{floor-1}) are exempt: they predate
// this lint, and the spec § 16 hard rules #7/#8 apply going forward. The
// floor itself must be raised whenever a future migration is authored that
// complies; lowering is forbidden once raised.
func TestRepoMigrationsPassLint(t *testing.T) {
	dir := repoMigrationsDir(t)
	v, err := ScanDir(dir, DefaultGrandfatherFloor)
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	if len(v) > 0 {
		t.Fatalf("repo migrations violate lint:\n%s", Aggregate(v))
	}
}

// repoMigrationsDir resolves the absolute path to migrations/ relative to
// this test file, so the test does not depend on the caller's CWD.
func repoMigrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations"))
}
