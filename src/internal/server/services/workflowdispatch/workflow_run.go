package workflowdispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WorkflowRunResult is the minimal subset of the GitHub Actions
// `workflow_run` response the reconciler consumes. Only the fields the
// classification table inspects survive — keep the surface small so the
// HTTP client doesn't drift behind GitHub's schema.
type WorkflowRunResult struct {
	Status     string
	Conclusion string
	StepName   string
	StepLog    string
}

// GetWorkflowRun queries `/repos/{owner}/{repo}/actions/runs/{run_id}`
// and returns the status + conclusion fields needed by the reconciler.
// Step-name / step-log are best-effort: GitHub returns them only when
// the run is completed and the failing step is well-known. Callers
// must handle empty values (the classifier falls back to ClassUnknown).
func (c *Client) GetWorkflowRun(ctx context.Context, runID int64) (WorkflowRunResult, error) {
	if c == nil {
		return WorkflowRunResult{}, fmt.Errorf("workflowdispatch: nil client")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d", c.apiBaseURL, c.owner, c.repo, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return WorkflowRunResult{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "0ops-server")

	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return WorkflowRunResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return WorkflowRunResult{}, fmt.Errorf("github workflow_run %d %d: %s", runID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var payload struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return WorkflowRunResult{}, err
	}
	return WorkflowRunResult{Status: payload.Status, Conclusion: payload.Conclusion}, nil
}
