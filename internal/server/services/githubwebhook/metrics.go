package githubwebhook

// Metric recorders are package-level function variables so the server
// binary can wire prometheus collectors without making this package depend
// on the observability package directly. Default no-op so unit tests do
// not need to inject stubs.
var (
	recordEventReceived  = func(event, status string) {}
	recordSignatureError = func(reason string) {}
)

// BindMetrics wires the recorders. Both arguments may be nil — passing nil
// reverts to the no-op default. Mirrors the cloudflare/observability
// binding pattern used elsewhere in this codebase.
func BindMetrics(eventRecorder func(event, status string), signatureRecorder func(reason string)) {
	if eventRecorder == nil {
		recordEventReceived = func(string, string) {}
	} else {
		recordEventReceived = eventRecorder
	}
	if signatureRecorder == nil {
		recordSignatureError = func(string) {}
	} else {
		recordSignatureError = signatureRecorder
	}
}

// RecordEvent is the public entrypoint the HTTP handler invokes after each
// successful Dispatch call.
func RecordEvent(event, status string) {
	recordEventReceived(event, status)
}

// RecordSignatureFailure records a verification failure with a stable
// reason label for the SLI in spec § 11 (`webhook_signature_invalid /
// total`).
func RecordSignatureFailure(reason string) {
	recordSignatureError(reason)
}
