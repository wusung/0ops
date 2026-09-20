// Command audit-rollover advances the audit_log partition window.
//
// It exists as a separate binary, not as a reconciler loop, because creating
// partitions is DDL: CREATE TABLE ... PARTITION OF plus the REVOKE that
// migration 00014 requires on every new partition. The long-running server
// connects as the append-only "0ops_app" role precisely so a leaked runtime
// credential cannot touch audit_log, and 00014 states outright that "rollover
// must run under a privileged role, not 0ops_app". Running this work in-process
// would mean handing the internet-facing server DDL credentials and dismantling
// that envelope for the sake of a monthly maintenance task.
//
// The server still watches the window: reconciler.AuditPartitionScanner reads
// the catalog under the restricted role and reports how many months remain, so
// a CronJob that silently stops running is visible long before audit writes
// start failing.
//
// Usage:
//
//	audit-rollover [-lookahead-months N] [-timeout DURATION]
//
// DATABASE_URL must hold privileged (migrate-level) credentials. APP_DATABASE_URL
// is ignored on purpose — falling back to it would fail at the first CREATE.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/audit"
)

func main() {
	lookahead := flag.Int("lookahead-months", 3, "how many future months to keep partitioned")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall deadline for the rollover")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(context.Background(), logger, *lookahead, *timeout); err != nil {
		logger.Error("audit rollover failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, lookahead int, timeout time.Duration) error {
	// Read DATABASE_URL directly rather than through db.ConfigFromEnv, which
	// prefers APP_DATABASE_URL: silently connecting as "0ops_app" here would
	// fail on the first CREATE TABLE with a permission error that looks like a
	// database problem rather than a misconfigured job.
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required (privileged credentials; APP_DATABASE_URL is not used)")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pool, err := db.NewPool(ctx, db.Config{URL: url})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	repo := db.NewRepository(pool)

	plan, err := audit.Rollover(ctx, repo, time.Now().UTC(), lookahead)
	if err != nil {
		return err
	}

	created := make([]string, 0, len(plan.Created))
	for _, m := range plan.Created {
		created = append(created, audit.PartitionLabel(m))
	}
	dropped := make([]string, 0, len(plan.Dropped))
	for _, m := range plan.Dropped {
		dropped = append(dropped, audit.PartitionLabel(m))
	}

	logger.Info("audit rollover complete",
		"ensured", created,
		"dropped", dropped,
		"archived", plan.Archived,
		"lookahead_months", lookahead,
	)
	return nil
}
