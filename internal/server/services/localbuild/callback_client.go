package localbuild

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CallbackEvent mirrors the body shape accepted by the production callback
// handler (internal/server/apps.go:applyDeployCallback). Per ADR-0012 § 4
// the HMAC contract is shared with production; no dev-only fields.
type CallbackEvent struct {
	Status                string  `json:"status"`
	ImageRef              string  `json:"image_ref,omitempty"`
	BuildMinutes          float64 `json:"build_minutes,omitempty"`
	ErrorSummary          string  `json:"error_summary,omitempty"`
	FailureClassification string  `json:"failure_classification,omitempty"`
}

// CallbackClient POSTs a CallbackEvent to the server's internal callback
// endpoint with the same HMAC envelope production GHA uses.
type CallbackClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewCallbackClient(baseURL, secret string, c *http.Client) *CallbackClient {
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	return &CallbackClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		http:    c,
	}
}

func (c *CallbackClient) Send(ctx context.Context, runID string, ev CallbackEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/internal/deploy-runs/%s/callback", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ops-Run-Id", runID)
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write(body)
	req.Header.Set("X-Ops-Signature", "hmac-sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("callback %s: %d", url, resp.StatusCode)
	}
	return nil
}
