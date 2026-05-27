package audit

// MetricObserver records audit_log write outcomes. The server wires a
// Prometheus-backed observer at startup; tests and headless callers
// receive a no-op via NopObserver.
type MetricObserver interface {
	ObserveWrite(action, outcome string)
	ObserveQuery(scope, outcome string)
	ObservePartitionRollover(outcome string)
}

// NopObserver returns an observer that discards every event. Useful
// for unit tests and binaries that have not wired Prometheus yet.
func NopObserver() MetricObserver { return nopObserver{} }

type nopObserver struct{}

func (nopObserver) ObserveWrite(string, string)             {}
func (nopObserver) ObserveQuery(string, string)             {}
func (nopObserver) ObservePartitionRollover(string)         {}

// ObserverFuncs lets a caller assemble an observer from inline
// functions without declaring a new type.
type ObserverFuncs struct {
	WriteFn             func(action, outcome string)
	QueryFn             func(scope, outcome string)
	PartitionRolloverFn func(outcome string)
}

// ObserveWrite implements MetricObserver.
func (o ObserverFuncs) ObserveWrite(action, outcome string) {
	if o.WriteFn != nil {
		o.WriteFn(action, outcome)
	}
}

// ObserveQuery implements MetricObserver.
func (o ObserverFuncs) ObserveQuery(scope, outcome string) {
	if o.QueryFn != nil {
		o.QueryFn(scope, outcome)
	}
}

// ObservePartitionRollover implements MetricObserver.
func (o ObserverFuncs) ObservePartitionRollover(outcome string) {
	if o.PartitionRolloverFn != nil {
		o.PartitionRolloverFn(outcome)
	}
}
