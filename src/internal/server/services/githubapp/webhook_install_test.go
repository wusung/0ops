package githubapp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestWebhookVerifierVerifyRequest(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "webhook-secret-12345")

	wv, err := NewWebhookVerifier()
	if err != nil {
		t.Fatalf("NewWebhookVerifier failed: %v", err)
	}

	// Test data
	payload := []byte(`{"action": "created", "installation": {"id": 12345}}`)

	// Compute correct signature
	expectedSig := wv.computeSignature(wv.currentSecret, payload)

	// Create request with valid signature
	body := io.NopCloser(bytes.NewReader(payload))
	req := &http.Request{
		Header: http.Header{
			GitHubWebhookSignatureHeader: []string{fmt.Sprintf("sha256=%s", expectedSig)},
		},
		Body: body,
	}

	// Verify should succeed
	body = io.NopCloser(bytes.NewReader(payload)) // Reset body
	req.Body = body

	verified, err := wv.VerifyRequest(req)
	if err != nil {
		t.Fatalf("VerifyRequest failed: %v", err)
	}

	if !bytes.Equal(verified, payload) {
		t.Error("verified body doesn't match original payload")
	}
}

func TestWebhookVerifierInvalidSignature(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "webhook-secret-12345")

	wv, err := NewWebhookVerifier()
	if err != nil {
		t.Fatalf("NewWebhookVerifier failed: %v", err)
	}

	payload := []byte(`{"action": "created", "installation": {"id": 12345}}`)

	// Create request with invalid signature
	body := io.NopCloser(bytes.NewReader(payload))
	req := &http.Request{
		Header: http.Header{
			GitHubWebhookSignatureHeader: []string{"sha256=invalid_signature_123456"},
		},
		Body: body,
	}

	_, err = wv.VerifyRequest(req)
	if err == nil {
		t.Error("expected VerifyRequest to fail with invalid signature")
	}
}

func TestWebhookVerifierMissingSignatureHeader(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "webhook-secret-12345")

	wv, err := NewWebhookVerifier()
	if err != nil {
		t.Fatalf("NewWebhookVerifier failed: %v", err)
	}

	payload := []byte(`{"action": "created"}`)

	// Create request without signature header
	body := io.NopCloser(bytes.NewReader(payload))
	req := &http.Request{
		Header: http.Header{},
		Body:   body,
	}

	_, err = wv.VerifyRequest(req)
	if err == nil {
		t.Error("expected VerifyRequest to fail with missing signature header")
	}
}

func TestWebhookVerifierPreviousSecret(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "current-secret-12345")
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET_PREVIOUS", "previous-secret-12345")

	wv, err := NewWebhookVerifier()
	if err != nil {
		t.Fatalf("NewWebhookVerifier failed: %v", err)
	}

	payload := []byte(`{"action": "created", "installation": {"id": 12345}}`)

	// Compute signature with previous secret
	prevSig := wv.computeSignature(wv.previousSecret, payload)

	// Create request with previous secret signature
	body := io.NopCloser(bytes.NewReader(payload))
	req := &http.Request{
		Header: http.Header{
			GitHubWebhookSignatureHeader: []string{fmt.Sprintf("sha256=%s", prevSig)},
		},
		Body: body,
	}

	// Verify should still succeed during rotation window
	verified, err := wv.VerifyRequest(req)
	if err != nil {
		t.Fatalf("VerifyRequest failed during secret rotation: %v", err)
	}

	if !bytes.Equal(verified, payload) {
		t.Error("verified body doesn't match original payload")
	}
}

func TestGetDeliveryID(t *testing.T) {
	deliveryID := "12345-delivery-id"
	req, err := http.NewRequest("POST", "http://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	req.Header.Set(GitHubWebhookDeliveryHeader, deliveryID)

	got := GetDeliveryID(req)
	if got != deliveryID {
		t.Errorf("GetDeliveryID mismatch: got %q, want %q", got, deliveryID)
	}
}

func TestGetDeliveryIDMissing(t *testing.T) {
	req := &http.Request{
		Header: http.Header{},
	}

	got := GetDeliveryID(req)
	if got != "" {
		t.Errorf("GetDeliveryID should return empty string for missing header: got %q", got)
	}
}

func TestNewWebhookVerifierMissingSecret(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "")

	_, err := NewWebhookVerifier()
	if err == nil {
		t.Error("expected NewWebhookVerifier to fail with missing secret")
	}
}

func TestWebhookVerifierComputeSignature(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "webhook-secret-12345")

	wv, err := NewWebhookVerifier()
	if err != nil {
		t.Fatalf("NewWebhookVerifier failed: %v", err)
	}

	payload := []byte("test payload")

	// Two calls with same payload should produce same signature
	sig1 := wv.computeSignature(wv.currentSecret, payload)
	sig2 := wv.computeSignature(wv.currentSecret, payload)

	if sig1 != sig2 {
		t.Errorf("signatures don't match: %q != %q", sig1, sig2)
	}

	// Different payload should produce different signature
	differentPayload := []byte("different payload")
	sig3 := wv.computeSignature(wv.currentSecret, differentPayload)

	if sig1 == sig3 {
		t.Errorf("different payloads produced same signature: %q", sig1)
	}
}
