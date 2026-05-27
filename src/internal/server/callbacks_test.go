package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDeployCallbackRecordsFailureClassificationMetric(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "test-webhook-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	var gotStage, gotClassification string
	prevRecorder := recordDeployFailureMetric
	recordDeployFailureMetric = func(stage, classification string) {
		gotStage = stage
		gotClassification = classification
	}
	t.Cleanup(func() {
		recordDeployFailureMetric = prevRecorder
	})

	body := `{"run_id":"deploy-1","status":"failed","trace_id":"trace-abc-123","failure_classification":"gitops_push_conflict"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("test-webhook-secret"))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}

	if gotStage != "callback" || gotClassification != "gitops_push_conflict" {
		t.Fatalf("failure metric not recorded as expected, got stage=%q classification=%q", gotStage, gotClassification)
	}
}

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

		// Legacy status mappings (backward compatibility)
		{"success", "live", true},
		{"failure", "failed", true},
		{"cancelled", "canceled", true},

		// Invalid states
		{"invalid", "", false},
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

func TestDeployCallbackDedupPreventsDuplicateDelivery(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "test-webhook-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := `{"run_id":"deploy-1","status":"live","trace_id":"trace-abc-123"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("test-webhook-secret"))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	deliveryID := "delivery-abc-123"

	// First callback delivery should succeed
	req1, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-0ops-Timestamp", ts)
	req1.Header.Set("X-0ops-Signature", sig)
	req1.Header.Set("X-0ops-Delivery-ID", deliveryID)

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first callback request error = %v", err)
	}
	defer func() { _ = resp1.Body.Close() }()
	if resp1.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp1.Body)
		t.Fatalf("first callback status = %d, body = %s", resp1.StatusCode, string(bodyText))
	}

	// Parse first response to confirm successful delivery
	var firstResp map[string]any
	if err := json.NewDecoder(resp1.Body).Decode(&firstResp); err != nil {
		t.Fatalf("decode first response error = %v", err)
	}
	if status, ok := firstResp["status"]; !ok || status != "ok" {
		t.Fatalf("first callback response status = %v, want ok", status)
	}

	// Second identical callback delivery should be flagged as duplicate
	req2, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/deploy-1/callback", strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() duplicate error = %v", err)
	}
	req2.Header = req1.Header.Clone()

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("duplicate callback request error = %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp2.Body)
		t.Fatalf("duplicate callback status = %d, body = %s", resp2.StatusCode, string(bodyText))
	}

	// Parse second response to confirm duplicate detection
	var secondResp map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&secondResp); err != nil {
		t.Fatalf("decode second response error = %v", err)
	}
	if status, ok := secondResp["status"]; !ok || status != "duplicate" {
		t.Fatalf("second callback response status = %v, want duplicate", status)
	}
}

func TestValidFailureClassification(t *testing.T) {
	tests := []string{
		"repo_checkout_failed",
		"build_timeout",
		"gitops_push_conflict",
		"unknown",
	}
	for _, v := range tests {
		if !isValidFailureClassification(v) {
			t.Errorf("isValidFailureClassification(%q) = false, want true", v)
		}
	}
	if isValidFailureClassification("invalid") {
		t.Fatal("isValidFailureClassification(\"invalid\") = true, want false")
	}
}
