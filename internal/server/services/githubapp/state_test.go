package githubapp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestStateSignerSignAndVerify(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "test-secret-12345")

	ss, err := NewStateSigner()
	if err != nil {
		t.Fatalf("NewStateSigner failed: %v", err)
	}

	teamID := "team-123"
	actorUserID := "user-456"
	previewID := "preview-789"

	// Sign
	state, err := ss.SignState(teamID, actorUserID, previewID)
	if err != nil {
		t.Fatalf("SignState failed: %v", err)
	}

	if state == "" {
		t.Error("state token is empty")
	}

	// Verify
	verifyTeamID, verifyActorUserID, verifyPreviewID, timestamp, err := ss.VerifyState(state)
	if err != nil {
		t.Fatalf("VerifyState failed: %v", err)
	}

	if verifyTeamID != teamID {
		t.Errorf("team_id mismatch: got %q, want %q", verifyTeamID, teamID)
	}

	if verifyActorUserID != actorUserID {
		t.Errorf("actor_user_id mismatch: got %q, want %q", verifyActorUserID, actorUserID)
	}

	if verifyPreviewID != previewID {
		t.Errorf("preview_id mismatch: got %q, want %q", verifyPreviewID, previewID)
	}

	// Timestamp should be close to now
	now := time.Now().Unix()
	if timestamp < now-5 || timestamp > now+5 {
		t.Errorf("timestamp out of range: got %d, now is %d", timestamp, now)
	}
}

func TestStateSignerInvalidSignature(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "test-secret-12345")

	ss, err := NewStateSigner()
	if err != nil {
		t.Fatalf("NewStateSigner failed: %v", err)
	}

	// Sign valid state
	state, err := ss.SignState("team-123", "user-456", "preview-789")
	if err != nil {
		t.Fatalf("SignState failed: %v", err)
	}

	// Tamper with state (change first char)
	tamperedState := string(rune(state[0]+1)) + state[1:]

	// Verify tampered state should fail
	_, _, _, _, err = ss.VerifyState(tamperedState)
	if err == nil {
		t.Error("expected VerifyState to fail for tampered state")
	}
}

func TestStateSignerExpired(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "test-secret-12345")

	ss, err := NewStateSigner()
	if err != nil {
		t.Fatalf("NewStateSigner failed: %v", err)
	}

	// Manually create expired state
	var data StateData
	data.TeamID = "team-123"
	data.ActorUserID = "user-456"
	data.PreviewID = "preview-789"
	data.Timestamp = time.Now().Unix() - (11 * 60) // 11 minutes ago
	data.Signature = ss.computeHMAC(ss.currentSecret,
		fmt.Sprintf("%s:%s:%s:%d", data.TeamID, data.ActorUserID, data.PreviewID, data.Timestamp))

	// Encode as state
	jsonBytes, _ := json.Marshal(data)
	expiredState := base64.StdEncoding.EncodeToString(jsonBytes)

	// Verify expired state should fail
	_, _, _, _, err = ss.VerifyState(expiredState)
	if err == nil {
		t.Error("expected VerifyState to fail for expired state")
	}
}

func TestStateSignerPreviousSecret(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "current-secret-12345")
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET_PREVIOUS", "previous-secret-12345")

	ss, err := NewStateSigner()
	if err != nil {
		t.Fatalf("NewStateSigner failed: %v", err)
	}

	// Create state with previous secret manually
	var data StateData
	data.TeamID = "team-123"
	data.ActorUserID = "user-456"
	data.PreviewID = "preview-789"
	data.Timestamp = time.Now().Unix()
	data.Signature = ss.computeHMAC(ss.previousSecret,
		fmt.Sprintf("%s:%s:%s:%d", data.TeamID, data.ActorUserID, data.PreviewID, data.Timestamp))

	// Encode as state
	jsonBytes, _ := json.Marshal(data)
	state := base64.StdEncoding.EncodeToString(jsonBytes)

	// Verify should still work with previous secret
	verifyTeamID, verifyActorUserID, verifyPreviewID, _, err := ss.VerifyState(state)
	if err != nil {
		t.Fatalf("VerifyState with previous secret failed: %v", err)
	}

	if verifyTeamID != "team-123" || verifyActorUserID != "user-456" || verifyPreviewID != "preview-789" {
		t.Error("verified values don't match")
	}
}

func TestNewStateSignerMissingSecret(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "")

	_, err := NewStateSigner()
	if err == nil {
		t.Error("expected NewStateSigner to fail with missing secret")
	}
}
