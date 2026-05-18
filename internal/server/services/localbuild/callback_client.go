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
	"strconv"
	"strings"
	"time"
)

// CallbackEvent mirrors the body shape accepted by the production callback
// handler (internal/server/apps.go::deployRunCallbackHandler). Field names
// match the deployCallbackRequest schema so the same handler validates both
// production GHA callbacks and dev LocalBuildDispatcher callbacks.
type CallbackEvent struct {
	RunID                 string  `json:"run_id"`
	Status                string  `json:"status"`
	TraceID               string  `json:"trace_id"`
	Image                 string  `json:"image,omitempty"`
	BuildMinutes          float64 `json:"build_minutes,omitempty"`
	ErrorSummary          string  `json:"error_summary,omitempty"`
	FailureClassification string  `json:"failure_classification,omitempty"`
	OpsToken              string  `json:"ops_token,omitempty"`
}

// CallbackClient POSTs a CallbackEvent to the server's internal callback
// endpoint with the same X-0ops-Timestamp / X-0ops-Signature envelope
// production GHA dispatch uses (apps.go::validateDeployCallbackSignature).
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
	ev.RunID = runID
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/internal/deploy-runs/%s/callback", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(c.secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", timestamp)
	req.Header.Set("X-0ops-Signature", signature)
	req.Header.Set("X-0ops-Delivery-ID", fmt.Sprintf("%s-%s-%s", runID, ev.Status, timestamp))
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
