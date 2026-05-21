package reconciler

// Observer is the metric hook surface the reconciler emits over. It
// decouples the runner from observability internals; cmd/server wires
// a concrete observer that forwards to the prometheus registry, while
// tests pass NopObserver().
type Observer interface {
	ObserveTick(kind string, outcome string)
	ObserveJobTerminal(kind string, outcome string)
	ObserveDeployTransition(from, to string)
	ObserveFailureClassification(classification string)
	ObserveIncidentOpened(kind, severity string)
	ObserveIncidentClosed(kind, severity string)
	SetPendingJobs(kind string, count int)
	SetOpenIncidents(severity string, count int)
	RecordUploadGC(processed, failed int)
}

// NopObserver returns an Observer whose methods are no-ops. Used by
// tests and by cmd/server when an explicit observer is not wired.
func NopObserver() Observer { return nopObserver{} }

type nopObserver struct{}

func (nopObserver) ObserveTick(string, string)                  {}
func (nopObserver) ObserveJobTerminal(string, string)           {}
func (nopObserver) ObserveDeployTransition(string, string)      {}
func (nopObserver) ObserveFailureClassification(string)         {}
func (nopObserver) ObserveIncidentOpened(string, string)        {}
func (nopObserver) ObserveIncidentClosed(string, string)        {}
func (nopObserver) SetPendingJobs(string, int)                  {}
func (nopObserver) SetOpenIncidents(string, int)                {}
func (nopObserver) RecordUploadGC(int, int)                     {}
