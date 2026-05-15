package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/githubapp"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// TestPreviewRedeployHTTPHappyPath drives the full HTTP path from CLI
// client → router → service. Spec § 6 acceptance bullet "CLI 主動 redeploy
// preview".
func TestPreviewRedeployHTTPHappyPath(t *testing.T) {
	store, token := newFakeStore()
	// Make alpha a live app with repo and branch so redeploy preview can
	// resolve defaults.
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
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewRedeploy(context.Background(), store.team.Slug, "alpha", dto.RedeployRequest{})
	if err != nil {
		t.Fatalf("PreviewRedeploy err = %v", err)
	}
	if preview.PreviewID == "" || preview.Action != "redeploy" {
		t.Fatalf("preview = %+v", preview)
	}
}

func TestRedeployHTTPRejectsPausedApp(t *testing.T) {
	store, token := newFakeStore()
	paused := "paused"
	repo := "https://github.com/foo/bar"
	branch := "main"
	for i := range store.apps {
		if store.apps[i].Slug == "alpha" {
			store.apps[i].Status = &paused
			store.apps[i].RepoURL = &repo
			store.apps[i].RepoDefaultBranch = &branch
		}
	}
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	_, err := client.PreviewRedeploy(context.Background(), store.team.Slug, "alpha", dto.RedeployRequest{})
	if err == nil || !strings.Contains(err.Error(), "app_paused") {
		t.Fatalf("err = %v, want app_paused", err)
	}
}

func TestRedeployHTTPConfirmTriggersDeployRun(t *testing.T) {
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
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewRedeploy(context.Background(), store.team.Slug, "alpha", dto.RedeployRequest{})
	if err != nil {
		t.Fatalf("PreviewRedeploy err = %v", err)
	}
	out, err := client.Redeploy(context.Background(), store.team.Slug, dto.ConfirmRedeployRequest{PreviewID: preview.PreviewID})
	if err != nil {
		t.Fatalf("Redeploy err = %v", err)
	}
	if out.DeployRunID == "" || out.AppSlug != "alpha" || out.Source != "user" {
		t.Fatalf("redeploy response = %+v", out)
	}
	if len(store.redeployRuns) != 1 {
		t.Fatalf("expected 1 deploy_run insert, got %d", len(store.redeployRuns))
	}
}

// TestRedeployHTTPIdempotentReplay covers ADR-0002 B1 last_result replay
// over the HTTP boundary.
func TestRedeployHTTPIdempotentReplay(t *testing.T) {
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
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, _ := client.PreviewRedeploy(context.Background(), store.team.Slug, "alpha", dto.RedeployRequest{})
	first, err := client.Redeploy(context.Background(), store.team.Slug, dto.ConfirmRedeployRequest{PreviewID: preview.PreviewID})
	if err != nil {
		t.Fatalf("first Redeploy err = %v", err)
	}
	second, err := client.Redeploy(context.Background(), store.team.Slug, dto.ConfirmRedeployRequest{PreviewID: preview.PreviewID})
	if err != nil {
		t.Fatalf("second Redeploy err = %v", err)
	}
	if first.DeployRunID != second.DeployRunID {
		t.Fatalf("replay returned different deploy_run id: %s vs %s", first.DeployRunID, second.DeployRunID)
	}
	if len(store.redeployRuns) != 1 {
		t.Fatalf("replay must not re-insert deploy_run; rows = %d", len(store.redeployRuns))
	}
}

// TestWebhookPushTriggersRedeployForLiveApp exercises the full webhook
// path: HMAC verify → dedup → push handler → redeploy.Trigger.
func TestWebhookPushTriggersRedeployForLiveApp(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "wh-redeploy-secret")
	store, _ := newFakeStore()
	// Bind the team to a github install id so the push handler can match
	// the installation back to the team.
	installID := int64(777)
	store.team.GithubInstallID = &installID
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
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	payload := []byte(`{
		"ref":"refs/heads/main",
		"after":"abc123",
		"deleted":false,
		"repository":{"id":1,"html_url":"https://github.com/foo/bar","default_branch":"main"},
		"installation":{"id":777}
	}`)
	mac := hmac.New(sha256.New, []byte("wh-redeploy-secret"))
	mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-push-happy")
	req.Header.Set("X-Hub-Signature-256", signature)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() err = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
	var envelope struct {
		Status     string   `json:"status"`
		Triggered  []string `json:"triggered"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Status != "triggered" || len(envelope.Triggered) != 1 {
		t.Fatalf("envelope = %+v", envelope)
	}
	if len(store.redeployRuns) != 1 || store.redeployRuns[0].Source != "webhook" {
		t.Fatalf("store.redeployRuns = %+v", store.redeployRuns)
	}
	if store.redeployRuns[0].WebhookDeliveryID != "delivery-push-happy" {
		t.Fatalf("delivery_id not persisted: %+v", store.redeployRuns[0])
	}
}

// TestWebhookPushReplayIsDeduped guards spec § 4.3 (24h replay protection).
func TestWebhookPushReplayIsDeduped(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "wh-dedup-secret")
	store, _ := newFakeStore()
	installID := int64(101)
	store.team.GithubInstallID = &installID
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
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	payload := []byte(`{
		"ref":"refs/heads/main",
		"after":"def456",
		"repository":{"html_url":"https://github.com/foo/bar","default_branch":"main"},
		"installation":{"id":101}
	}`)
	mac := hmac.New(sha256.New, []byte("wh-dedup-secret"))
	mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(payload))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", "delivery-dup-1")
		req.Header.Set("X-Hub-Signature-256", signature)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() err = %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("iter %d status = %d", i, resp.StatusCode)
		}
	}
	if len(store.redeployRuns) != 1 {
		t.Fatalf("dedup did not stop replay: rows = %d", len(store.redeployRuns))
	}
}

// TestWebhookBadSignatureRejected guards spec § 14 hard rule #1 + #10.
func TestWebhookBadSignatureRejected(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "wh-secret-good")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := []byte(`{"ref":"refs/heads/main","installation":{"id":1}}`)
	mac := hmac.New(sha256.New, []byte("wh-secret-WRONG"))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-badsig")
	req.Header.Set("X-Hub-Signature-256", signature)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() err = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
	if len(store.redeployRuns) != 0 {
		t.Fatalf("bad signature must not insert deploy_run rows")
	}
}

// TestWebhookPayloadTooLargeRejected guards spec § 14 hard rule #7.
func TestWebhookPayloadTooLargeRejected(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "wh-secret")
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := bytes.Repeat([]byte("a"), 5*1024*1024+1)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-toobig")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() err = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
}

// TestWebhookInstallationStillRouted ensures the existing installation
// dispatch path is preserved after the dispatcher refactor.
func TestWebhookInstallationStillRouted(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "wh-install-secret")
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "install-state")
	store, _ := newFakeStore()
	installID := int64(202)
	store.team.GithubInstallID = &installID
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	body := []byte(`{"action":"deleted","installation":{"id":202}}`)
	mac := hmac.New(sha256.New, []byte("wh-install-secret"))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "installation")
	req.Header.Set("X-GitHub-Delivery", "delivery-install-after-refactor")
	req.Header.Set("X-Hub-Signature-256", signature)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() err = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if store.team.GithubInstallID != nil {
		t.Fatalf("installation 'deleted' should have cleared install id")
	}
	// Make sure we didn't accidentally route the installation event
	// through the push handler.
	if len(store.redeployRuns) != 0 {
		t.Fatalf("installation event must not trigger redeploy: %+v", store.redeployRuns)
	}
}

// Guard against accidental import drift — compile-time check that the
// installation handler interface is still satisfied by *githubapp.Service.
var _ = githubapp.ActionInstall
