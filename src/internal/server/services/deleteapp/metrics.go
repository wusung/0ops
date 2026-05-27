package deleteapp

// MetricRecorders are the hooks the HTTP handler uses to emit Prometheus
// counters / histograms. The wiring lives in internal/server/observability;
// this package only exposes the function signatures.
type MetricRecorders struct {
	PreviewOutcome func(outcome string)
	ConfirmOutcome func(outcome string, idempotentReplay bool)
	ResidueOutcome func(outcome string)
}

// NoopRecorders returns a MetricRecorders whose hooks are no-ops; useful
// in tests and dev binaries that don't run the metrics binder.
func NoopRecorders() MetricRecorders {
	return MetricRecorders{
		PreviewOutcome: func(string) {},
		ConfirmOutcome: func(string, bool) {},
		ResidueOutcome: func(string) {},
	}
}
