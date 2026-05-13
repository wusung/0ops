package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestDeployCallbackSignatureValidationRejectsInvalidSignature(t *testing.T) {
	// Test case 1: Invalid signature format (not sha256= prefixed)
	result := validateCallbackSignature("test-secret", "1234567890", []byte(`{"status":"success"}`), "invalid")
	if result {
		t.Fatalf("expected signature validation to fail for malformed signature, got true")
	}

	// Test case 2: Invalid signature format (wrong prefix)
	result = validateCallbackSignature("test-secret", "1234567890", []byte(`{"status":"success"}`), "md5=abcd1234")
	if result {
		t.Fatalf("expected signature validation to fail for wrong prefix, got true")
	}

	// Test case 3: Invalid hex string after sha256= prefix
	result = validateCallbackSignature("test-secret", "1234567890", []byte(`{"status":"success"}`), "sha256=not-a-hex")
	if result {
		t.Fatalf("expected signature validation to fail for invalid hex, got true")
	}

	// Test case 4: Valid format but wrong signature value
	result = validateCallbackSignature("test-secret", "1234567890", []byte(`{"status":"success"}`), "sha256=0000000000000000000000000000000000000000000000000000000000000000")
	if result {
		t.Fatalf("expected signature validation to fail for mismatched signature, got true")
	}
}

func TestDeployCallbackSignatureValidationAcceptsValidSignature(t *testing.T) {
	// Generate correct signature
	secret := "test-signing-secret"
	timestamp := "1234567890"
	body := []byte(`{"run_id":"test-run-id","status":"success"}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	correctSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// Validate correct signature
	result := validateCallbackSignature(secret, timestamp, body, correctSig)
	if !result {
		t.Fatalf("expected signature validation to pass for correct signature, got false")
	}
}
