package githubapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// GitHubWebhookSignatureHeader is the header name for GitHub webhook signature.
const GitHubWebhookSignatureHeader = "X-Hub-Signature-256"

// GitHubWebhookDeliveryHeader is the header name for GitHub webhook delivery ID.
const GitHubWebhookDeliveryHeader = "X-GitHub-Delivery"

// WebhookVerifier verifies GitHub webhook signatures and deduplicates webhooks.
type WebhookVerifier struct {
	currentSecret  string
	previousSecret string
}

// NewWebhookVerifier creates a new WebhookVerifier from environment variables.
// Supports current and previous secrets for rotation (90d window, 30min dual).
// Env vars: OPS_GITHUB_WEBHOOK_SECRET (current)
//
//	OPS_GITHUB_WEBHOOK_SECRET_PREVIOUS (previous)
func NewWebhookVerifier() (*WebhookVerifier, error) {
	current := strings.TrimSpace(os.Getenv("OPS_GITHUB_WEBHOOK_SECRET"))
	if current == "" {
		return nil, fmt.Errorf("missing OPS_GITHUB_WEBHOOK_SECRET")
	}

	previous := strings.TrimSpace(os.Getenv("OPS_GITHUB_WEBHOOK_SECRET_PREVIOUS"))

	return &WebhookVerifier{
		currentSecret:  current,
		previousSecret: previous,
	}, nil
}

// VerifyRequest verifies the GitHub webhook signature in an HTTP request.
// It reads the body and returns the body bytes for further processing.
// Signature format: "sha256=<hex>"
func (wv *WebhookVerifier) VerifyRequest(r *http.Request) ([]byte, error) {
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Get signature from header
	signature := r.Header.Get(GitHubWebhookSignatureHeader)
	if signature == "" {
		return nil, fmt.Errorf("missing %s header", GitHubWebhookSignatureHeader)
	}

	// Verify signature against current and previous secrets
	if !wv.verifySignature(body, signature) {
		return nil, fmt.Errorf("invalid webhook signature")
	}

	return body, nil
}

// GetDeliveryID extracts the delivery ID from the request headers.
func GetDeliveryID(r *http.Request) string {
	return r.Header.Get(GitHubWebhookDeliveryHeader)
}

// verifySignature verifies the webhook signature against both current and previous secrets.
func (wv *WebhookVerifier) verifySignature(body []byte, signature string) bool {
	// Extract hex string from "sha256=..." format
	sigHex := strings.TrimPrefix(signature, "sha256=")
	if sigHex == signature {
		// Not in expected format
		return false
	}

	// Verify against current secret
	if wv.computeSignature(wv.currentSecret, body) == sigHex {
		return true
	}

	// Try previous secret if set
	if wv.previousSecret != "" && wv.computeSignature(wv.previousSecret, body) == sigHex {
		return true
	}

	return false
}

// computeSignature computes the SHA256 HMAC and returns hex string.
func (wv *WebhookVerifier) computeSignature(secret string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
