package workflowdispatch

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var (
	ErrInvalidOpsTokenFormat    = errors.New("invalid ops token format")
	ErrInvalidOpsTokenSignature = errors.New("invalid ops token signature")
	ErrExpiredOpsToken          = errors.New("expired ops token")
	ErrMissingOpsTokenSecret    = errors.New("missing OPS_TOKEN_SIGNING_SECRET")
)

// OpsTokenPayload is the signed short-lived deploy credential.
type OpsTokenPayload struct {
	RunID     string    `json:"run_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Scopes    []string  `json:"scopes"`
	TraceID   string    `json:"trace_id"`
}

// OpsTokenSigner signs and verifies ephemeral deploy tokens.
type OpsTokenSigner struct {
	secret []byte
	now    func() time.Time
}

// NewOpsTokenSignerFromEnv loads the signer secret from OPS_TOKEN_SIGNING_SECRET.
func NewOpsTokenSignerFromEnv() (*OpsTokenSigner, error) {
	secret := strings.TrimSpace(os.Getenv("OPS_TOKEN_SIGNING_SECRET"))
	if secret == "" {
		return nil, ErrMissingOpsTokenSecret
	}
	return &OpsTokenSigner{secret: []byte(secret), now: time.Now}, nil
}

// Issue returns a signed ops token with a fixed 20 minute TTL.
func (s *OpsTokenSigner) Issue(runID, traceID string, scopes []string) (string, error) {
	if s == nil || len(s.secret) == 0 {
		return "", ErrMissingOpsTokenSecret
	}
	payload := OpsTokenPayload{
		RunID:     runID,
		ExpiresAt: s.now().UTC().Add(20 * time.Minute),
		Scopes:    append([]string(nil), scopes...),
		TraceID:   traceID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(bodyB64))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return bodyB64 + "." + sigB64, nil
}

// ParseOpsToken verifies and decodes an ops token.
func ParseOpsToken(token string, secret []byte) (*OpsTokenPayload, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidOpsTokenFormat
	}

	bodyBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidOpsTokenFormat
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	wantSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(wantSig), []byte(parts[1])) {
		return nil, ErrInvalidOpsTokenSignature
	}

	var payload OpsTokenPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, fmt.Errorf("decode ops token payload: %w", err)
	}
	if time.Now().UTC().After(payload.ExpiresAt) {
		return nil, ErrExpiredOpsToken
	}
	return &payload, nil
}
