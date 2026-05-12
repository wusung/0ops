package githubapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// StateData holds the claims bundled in an install state token.
type StateData struct {
	TeamID      string `json:"team_id"`
	ActorUserID string `json:"actor_user_id"`
	PreviewID   string `json:"preview_id"`
	Timestamp   int64  `json:"timestamp"` // Unix seconds
	Signature   string `json:"signature"` // hex-encoded HMAC
}

// StateSigner signs and verifies GitHub App install state tokens.
// state token format: base64(JSON {team_id, actor_user_id, preview_id, timestamp, signature})
// signature = hex(HMAC-SHA256(secret, team_id + ":" + actor_user_id + ":" + preview_id + ":" + timestamp))
type StateSigner struct {
	currentSecret  string
	previousSecret string
}

// NewStateSigner creates a new StateSigner from environment variables.
// Supports current and previous secrets for rotation (90d window, 30min dual).
// Env vars: OPS_GITHUB_APP_STATE_HMAC_SECRET (current)
//
//	OPS_GITHUB_APP_STATE_HMAC_SECRET_PREVIOUS (previous)
func NewStateSigner() (*StateSigner, error) {
	current := strings.TrimSpace(os.Getenv("OPS_GITHUB_APP_STATE_HMAC_SECRET"))
	if current == "" {
		return nil, fmt.Errorf("missing OPS_GITHUB_APP_STATE_HMAC_SECRET")
	}

	previous := strings.TrimSpace(os.Getenv("OPS_GITHUB_APP_STATE_HMAC_SECRET_PREVIOUS"))

	return &StateSigner{
		currentSecret:  current,
		previousSecret: previous,
	}, nil
}

// SignState signs a state token with current secret (10 min TTL).
func (ss *StateSigner) SignState(teamID, actorUserID, previewID string) (string, error) {
	now := time.Now().Unix()
	data := StateData{
		TeamID:      teamID,
		ActorUserID: actorUserID,
		PreviewID:   previewID,
		Timestamp:   now,
	}

	// Compute HMAC over "team_id:actor_user_id:preview_id:timestamp"
	message := fmt.Sprintf("%s:%s:%s:%d", teamID, actorUserID, previewID, now)
	sig := ss.computeHMAC(ss.currentSecret, message)
	data.Signature = sig

	// Encode as base64 JSON
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal state: %w", err)
	}

	return base64.StdEncoding.EncodeToString(jsonBytes), nil
}

// VerifyState verifies a state token and checks TTL (10 min).
// Returns (teamID, actorUserID, previewID, timestamp, error).
func (ss *StateSigner) VerifyState(state string) (string, string, string, int64, error) {
	// Decode base64
	jsonBytes, err := base64.StdEncoding.DecodeString(state)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("decode state: %w", err)
	}

	// Unmarshal JSON
	var data StateData
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return "", "", "", 0, fmt.Errorf("unmarshal state: %w", err)
	}

	// Verify signature against current and previous secrets
	message := fmt.Sprintf("%s:%s:%s:%d", data.TeamID, data.ActorUserID, data.PreviewID, data.Timestamp)

	currentSig := ss.computeHMAC(ss.currentSecret, message)
	if !hmac.Equal([]byte(data.Signature), []byte(currentSig)) {
		// Try previous secret if set
		if ss.previousSecret != "" {
			prevSig := ss.computeHMAC(ss.previousSecret, message)
			if !hmac.Equal([]byte(data.Signature), []byte(prevSig)) {
				return "", "", "", 0, fmt.Errorf("invalid state signature")
			}
		} else {
			return "", "", "", 0, fmt.Errorf("invalid state signature")
		}
	}

	// Check TTL (10 minutes)
	now := time.Now().Unix()
	ttl := 10 * time.Minute
	if now-data.Timestamp > int64(ttl.Seconds()) {
		return "", "", "", 0, fmt.Errorf("state expired")
	}

	return data.TeamID, data.ActorUserID, data.PreviewID, data.Timestamp, nil
}

// computeHMAC computes HMAC-SHA256 and returns hex-encoded result.
func (ss *StateSigner) computeHMAC(secret, message string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}
