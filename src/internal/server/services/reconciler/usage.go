package reconciler

import "context"

// UsageScanner reconciles the allocation ledger against the cluster.
// *usage.Reconciler satisfies it; kept as a local interface so this
// package does not depend on the usage package's types.
//
// The ledger's accuracy does not come from how often this runs: interval
// boundaries are read off the pod objects themselves. The interval only
// bounds how late a vanished pod is noticed, and that error always
// under-bills (resource-usage-metering spec § 4.4).
type UsageScanner interface {
	Tick(ctx context.Context) (opened, closed int, err error)
}

// UsageRollupScanner integrates closed days into permanent totals and
// expires the raw intervals behind them. *usage.Rollupper satisfies it.
//
// Hourly rather than daily so a backend that was down at midnight still
// closes the books promptly, and so the lag gauge stays meaningful.
type UsageRollupScanner interface {
	Tick(ctx context.Context) (days int, err error)
}

// UsageSampleScanner records instantaneous consumption. *usage.Sampler
// satisfies it.
//
// This is the observation track, not metering: it answers "is this app
// sized sensibly". If it stops working the allocation ledger is
// unaffected, which is why a failed tick is reported rather than
// escalated.
type UsageSampleScanner interface {
	Tick(ctx context.Context) (written int, err error)
	ConsecutiveFailures() int
}
