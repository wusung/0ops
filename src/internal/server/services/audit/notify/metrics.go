package notify

// MetricObserver is the observability seam for the notify subsystem. Delivery
// outcomes live here + on webhook_delivery, never in audit_log (spec § 7.5,
// § 13). Implementations must be safe for concurrent use.
type MetricObserver interface {
	// ObserveEnqueue records an enqueue outcome: "enqueued", "skipped",
	// "panic_isolated", or "error".
	ObserveEnqueue(outcome string)
	// ObserveDelivery records a delivery attempt terminal outcome:
	// "succeeded", "failed", "dropped".
	ObserveDelivery(event, outcome string)
	// ObserveCircuitBreaker records a subscription auto-disable.
	ObserveCircuitBreaker()
}

// NopObserver returns a no-op MetricObserver for callers that pass nil.
func NopObserver() MetricObserver { return nopObserver{} }

type nopObserver struct{}

func (nopObserver) ObserveEnqueue(string)          {}
func (nopObserver) ObserveDelivery(string, string) {}
func (nopObserver) ObserveCircuitBreaker()         {}
