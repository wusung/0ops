package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
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

func TestDeployCallbackTimestampValidationRejectsStaleTimestamp(t *testing.T) {
	// 時間戳超過 5 分鐘
	oldTimestamp := time.Now().UTC().Add(-6 * time.Minute)
	result := validateCallbackTimestamp(
		strconv.FormatInt(oldTimestamp.Unix(), 10),
		time.Now().UTC(),
		5*time.Minute,
	)
	if result {
		t.Fatal("expected stale timestamp to be rejected")
	}
}

func TestDeployCallbackTimestampValidationAcceptsRecentTimestamp(t *testing.T) {
	// 時間戳在窗口內
	recentTimestamp := time.Now().UTC().Add(-2 * time.Minute)
	result := validateCallbackTimestamp(
		strconv.FormatInt(recentTimestamp.Unix(), 10),
		time.Now().UTC(),
		5*time.Minute,
	)
	if !result {
		t.Fatal("expected recent timestamp to be accepted")
	}
}

func TestDeployCallbackTimestampValidationRejectsFutureTimestamp(t *testing.T) {
	// 時間戳超過 5 分鐘在未來
	futureTimestamp := time.Now().UTC().Add(6 * time.Minute)
	result := validateCallbackTimestamp(
		strconv.FormatInt(futureTimestamp.Unix(), 10),
		time.Now().UTC(),
		5*time.Minute,
	)
	if result {
		t.Fatal("expected future timestamp to be rejected")
	}
}

func TestDeployCallbackTimestampValidationAcceptsExactBoundaryTimestamp(t *testing.T) {
	// 時間戳正好 5 分鐘（邊界應接受）
	now := time.Now().UTC().Truncate(time.Second)
	boundaryTimestamp := now.Add(-5 * time.Minute)
	result := validateCallbackTimestamp(
		strconv.FormatInt(boundaryTimestamp.Unix(), 10),
		now,
		5*time.Minute,
	)
	if !result {
		t.Fatal("expected boundary timestamp (exactly 5 min) to be accepted")
	}
}

func TestNormalizeDeployStatusHandlesAllValidStates(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		valid    bool
	}{
		// All 10 required states
		{"queued", "queued", true},
		{"preparing", "preparing", true},
		{"building", "building", true},
		{"pushing", "pushing", true},
		{"rendering", "rendering", true},
		{"syncing", "syncing", true},
		{"live", "live", true},
		{"failed", "failed", true},
		{"canceled", "canceled", true},
		{"rolled_back", "rolled_back", true},

		// Mixed case variations
		{"QUEUED", "queued", true},
		{"Preparing", "preparing", true},
		{"BUILDING", "building", true},
		{"Pushing", "pushing", true},
		{"RENDERING", "rendering", true},
		{"Syncing", "syncing", true},
		{"LIVE", "live", true},
		{"Failed", "failed", true},
		{"CANCELED", "canceled", true},
		{"Rolled_Back", "rolled_back", true},

		// Whitespace handling
		{"  queued  ", "queued", true},
		{"\tbuilding\t", "building", true},
		{"\n  live  \n", "live", true},

		// Invalid states
		{"invalid", "", false},
		{"success", "", false},
		{"failure", "", false},
		{"cancelled", "", false},
		{"", "", false},
		{"   ", "", false},
	}

	for _, tt := range tests {
		status, ok := normalizeDeployStatus(tt.input)
		if ok != tt.valid || (ok && status != tt.expected) {
			t.Errorf("normalizeDeployStatus(%q): got (%q, %v), want (%q, %v)",
				tt.input, status, ok, tt.expected, tt.valid)
		}
	}
}
