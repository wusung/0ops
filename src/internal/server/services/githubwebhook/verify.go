package githubwebhook

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// MaxPayloadBytes caps the request body size accepted by the dispatcher
// (spec § 4.2 step 1; § 14 hard rule #7). GitHub's documented upper bound
// is 5 MB; over that we 400 without verifying signature.
const MaxPayloadBytes = 5 * 1024 * 1024

// PayloadTooLargeError is returned by ReadBoundedBody when the request
// exceeds MaxPayloadBytes. Kept distinct so the handler can map to the
// `webhook_payload_too_large` code from spec § 9.
type PayloadTooLargeError struct{}

func (PayloadTooLargeError) Error() string { return "github webhook payload too large" }

// SignatureVerifier is the contract the dispatcher uses to authenticate a
// request. It returns the raw body bytes on success so the dispatcher can
// re-parse without re-reading. Implementation lives in
// internal/server/services/githubapp/webhook_install.go and is wired by
// the existing factory so secret rotation lands once for both flows.
type SignatureVerifier interface {
	VerifyRequest(r *http.Request) ([]byte, error)
}

// ReadBoundedBody enforces MaxPayloadBytes BEFORE delegating to the
// SignatureVerifier. We read up to MaxPayloadBytes+1 to detect overflow;
// if the request is small we hand the buffered body back as a fresh
// io.NopCloser so the verifier (which calls io.ReadAll) sees exactly the
// same bytes we measured.
func ReadBoundedBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, fmt.Errorf("github webhook: missing body")
	}
	limited := io.LimitReader(r.Body, MaxPayloadBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("github webhook: read body: %w", err)
	}
	if len(body) > MaxPayloadBytes {
		return nil, PayloadTooLargeError{}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
