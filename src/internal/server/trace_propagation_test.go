package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	tracemw "github.com/winshare/zeroops/internal/server/middleware/trace"
	"github.com/winshare/zeroops/internal/server/services/redeploy"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// recordingTraceDispatcher captures the workflow_dispatch payload Trigger
// hands to the dispatcher so the test can assert the GHA-bound trace_id
// matches the inbound HTTP X-Trace-ID header.
type recordingTraceDispatcher struct {
	payloads []workflowdispatch.ClientPayload
}

func (r *recordingTraceDispatcher) Dispatch(_ context.Context, p workflowdispatch.ClientPayload) error {
	r.payloads = append(r.payloads, p)
	return nil
}

// stubOpsTokenSigner returns a deterministic token; the test never inspects
// it but Trigger refuses to dispatch when the signer is nil.
type stubOpsTokenSigner struct{}

func (stubOpsTokenSigner) Issue(_, _ string, _ []string) (string, error) {
	return "stub-ops-token", nil
}

// TestTracePropagationFullChain proves a trace_id supplied in the inbound
// X-Trace-ID header survives every hop: middleware ctx → preview creation
// (Task 2 db-level) → confirmRedeployHandler → redeploy.Trigger → the
// workflow_dispatch payload handed to the dispatcher → the audit_log row
// written by deployRunCallbackHandler when the GHA callback returns.
//
// Each hop is covered individually by Tasks 2-4; this test locks the
// composition. Per the trace_id-end-to-end plan Task 5: only the golden
// path is exercised — the C3 negative case (payload trace_id wins over
// the request header in audit.Log) is already pinned by
// TestDeployCallbackWritesAuditLogWithPayloadTraceID.
func TestTracePropagationFullChain(t *testing.T) {
	const headerTrace = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"

	t.Setenv("OPS_CALLBACK_SECRET", "trace-e2e-callback-secret")

	// Build fakeStore with alpha as a live app — required so the redeploy
	// preview can resolve commit/ref defaults (see
	// TestRedeployHTTPConfirmTriggersDeployRun).
	store, token := newFakeStore()
	status := "live"
	repo := "https://github.com/foo/bar"
	branch := "main"
	for i := range store.apps {
		if store.apps[i].Slug == "alpha" {
			store.apps[i].Status = &status
			store.apps[i].RepoURL = &repo
			store.apps[i].RepoDefaultBranch = &branch
		}
	}

	// Override the package-level redeploy dispatcher / signer factories so
	// the production newRedeployTriggerFromEnv produces a Trigger backed by
	// our recording dispatcher. This is the documented test extension point
	// in redeploy.go:44-47.
	disp := &recordingTraceDispatcher{}
	prevDispatcher := redeployDispatcher
	prevSigner := redeployTokenSigner
	redeployDispatcher = func() redeploy.Dispatcher { return disp }
	redeployTokenSigner = func() redeploy.OpsTokenSigner { return stubOpsTokenSigner{} }
	t.Cleanup(func() {
		redeployDispatcher = prevDispatcher
		redeployTokenSigner = prevSigner
	})

	// Mount the full router with a fake audit writer wired into the
	// callback handler; wrap with the trace-injection middleware so the
	// inbound X-Trace-ID lands in ctx for downstream handlers.
	auditWriter := &fakeAuditWriter{}
	router := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, nil, nil, nil, auditWriter)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(tracemw.Middleware(logger)(router))
	t.Cleanup(srv.Close)

	// Step 1 — Preview redeploy with X-Trace-ID header. The handler runs
	// CreatePreview which (in production, via db/members.go) stores the ctx
	// trace_id; the fakeStore drops it because Task 2's preview.trace_id
	// persistence is verified by TestCreatePreviewPersistsTraceIDFromContext.
	previewID := postRedeployPreview(t, srv.URL, token, store.team.Slug, "alpha", headerTrace)

	// Step 2 — Confirm redeploy with the same X-Trace-ID. confirmRedeployHandler
	// reads audit.TraceIDFromContext, which surfaces the value our
	// traceCtxMiddleware injected from the X-Trace-ID header — no
	// X-Request-Id workaround required.
	runID := postRedeployConfirm(t, srv.URL, token, store.team.Slug, previewID, headerTrace)
	if runID == "" {
		t.Fatalf("confirm returned empty deploy_run_id")
	}

	// Assert the workflow_dispatch payload carries the trace_id, proving the
	// middleware → handler → Trigger → dispatcher hop is intact.
	if len(disp.payloads) != 1 {
		t.Fatalf("dispatcher payloads = %d, want 1", len(disp.payloads))
	}
	if got := disp.payloads[0].TraceID; got != headerTrace {
		t.Fatalf("workflow payload trace_id = %q, want %q", got, headerTrace)
	}
	if got := disp.payloads[0].RunID; got != runID {
		t.Fatalf("workflow payload run_id = %q, want %q", got, runID)
	}

	// Step 3 — Deliver the GHA callback for the freshly minted run with the
	// same trace_id in the payload. deployRunCallbackHandler must write
	// exactly one audit_log row tagged with the payload trace_id, closing
	// the chain.
	postDeployCallback(t, srv.URL, "trace-e2e-callback-secret", runID, headerTrace)

	if len(auditWriter.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(auditWriter.entries))
	}
	entry := auditWriter.entries[0]
	if entry.Action != "deploy_callback" {
		t.Errorf("Action = %q, want deploy_callback", entry.Action)
	}
	if entry.SubjectID == nil || *entry.SubjectID != runID {
		t.Errorf("SubjectID = %v, want pointer to %q", entry.SubjectID, runID)
	}
	if got := auditWriter.traceIDs[0]; got != headerTrace {
		t.Fatalf("audit ctx trace_id = %q, want %q (full chain broken)", got, headerTrace)
	}
}

// postRedeployPreview sends POST /v1/teams/{slug}/apps/{app}/redeploys:preview
// with the bearer token + X-Trace-ID header. No X-Request-Id is sent —
// the chain must compose from X-Trace-ID alone. Returns the preview_id
// from the JSON envelope.
func postRedeployPreview(t *testing.T, baseURL, token, teamSlug, appSlug, traceID string) string {
	t.Helper()
	url := baseURL + "/v1/teams/" + teamSlug + "/apps/" + appSlug + "/redeploys:preview"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("preview NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Trace-ID", traceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", resp.StatusCode, string(body))
	}
	var out struct {
		PreviewID string `json:"preview_id"`
		Action    string `json:"action"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("preview decode: %v, body = %s", err, string(body))
	}
	if out.PreviewID == "" || out.Action != "redeploy" {
		t.Fatalf("preview envelope = %s", string(body))
	}
	return out.PreviewID
}

// postRedeployConfirm sends POST /v1/teams/{slug}/redeploys and returns the
// deploy_run_id. Only X-Trace-ID is sent; the handler reads the trace via
// audit.TraceIDFromContext, so X-Request-Id is not required.
func postRedeployConfirm(t *testing.T, baseURL, token, teamSlug, previewID, traceID string) string {
	t.Helper()
	url := baseURL + "/v1/teams/" + teamSlug + "/redeploys"
	body, err := json.Marshal(map[string]string{"preview_id": previewID})
	if err != nil {
		t.Fatalf("confirm marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("confirm NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Trace-ID", traceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("confirm Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		DeployRunID string `json:"deploy_run_id"`
		AppSlug     string `json:"app_slug"`
		Source      string `json:"source"`
		TraceID     string `json:"trace_id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		t.Fatalf("confirm decode: %v, body = %s", err, string(respBody))
	}
	if out.AppSlug != "alpha" || out.Source != "user" {
		t.Fatalf("confirm envelope = %s", string(respBody))
	}
	if out.TraceID != traceID {
		t.Fatalf("confirm response trace_id = %q, want %q", out.TraceID, traceID)
	}
	return out.DeployRunID
}

// postDeployCallback signs and delivers a successful GHA callback for runID
// using OPS_CALLBACK_SECRET (matches callbacks_test.go:34-37 pattern).
func postDeployCallback(t *testing.T, baseURL, secret, runID, traceID string) {
	t.Helper()
	body := `{"run_id":"` + runID + `","status":"success","trace_id":"` + traceID + `"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + "." + body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/internal/deploy-runs/"+runID+"/callback",
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("callback NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)
	// Intentionally do NOT set X-Trace-ID here — the audit row's trace_id
	// must come from the JSON payload, not the request header (C3 contract).

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		bodyText, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(bodyText))
	}
}
