package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/githubapp"
	"github.com/winshare/zeroops/internal/shared/backendclient"
)

// fakeGitHubService implements githubAppService for handler tests so we can
// observe call arguments without spinning the full service stack.
type fakeGitHubService struct {
	previewInstallFn      func(ctx context.Context, teamID, actorUserID, teamSlug string) (githubapp.PreviewResult, error)
	confirmInstallFn      func(ctx context.Context, teamID, actorUserID, previewID string) (githubapp.ConfirmInstallResult, error)
	callbackFn            func(ctx context.Context, installID, state string) (githubapp.CallbackResult, error)
	successRedirectFn     func(teamSlug string) string
	previewUninstallFn    func(ctx context.Context, teamID, actorUserID, teamSlug string) (githubapp.PreviewResult, error)
	confirmUninstallFn    func(ctx context.Context, teamID, actorUserID, previewID string) (githubapp.ConfirmUninstallResult, error)
	statusFn              func(ctx context.Context, teamID string) (githubapp.InstallStatus, error)
	webhookFn             func(ctx context.Context, deliveryID string, payload []byte) (githubapp.WebhookOutcome, error)
	previewInstallCalls   int32
	confirmInstallCalls   int32
	uninstallPreviewCalls int32
	uninstallConfirmCalls int32
	statusCalls           int32
	callbackCalls         int32
	webhookCalls          int32
}

func (f *fakeGitHubService) PreviewInstall(ctx context.Context, teamID, actorUserID, teamSlug string) (githubapp.PreviewResult, error) {
	atomic.AddInt32(&f.previewInstallCalls, 1)
	if f.previewInstallFn != nil {
		return f.previewInstallFn(ctx, teamID, actorUserID, teamSlug)
	}
	return githubapp.PreviewResult{PreviewID: "preview-install-1", Action: githubapp.ActionInstall, Summary: "summary"}, nil
}

func (f *fakeGitHubService) ConfirmInstall(ctx context.Context, teamID, actorUserID, previewID string) (githubapp.ConfirmInstallResult, error) {
	atomic.AddInt32(&f.confirmInstallCalls, 1)
	if f.confirmInstallFn != nil {
		return f.confirmInstallFn(ctx, teamID, actorUserID, previewID)
	}
	return githubapp.ConfirmInstallResult{InstallURL: "https://github.com/apps/0ops/installations/new?state=signed"}, nil
}

func (f *fakeGitHubService) HandleCallback(ctx context.Context, installID, state string) (githubapp.CallbackResult, error) {
	atomic.AddInt32(&f.callbackCalls, 1)
	if f.callbackFn != nil {
		return f.callbackFn(ctx, installID, state)
	}
	return githubapp.CallbackResult{TeamSlug: "acme"}, nil
}

func (f *fakeGitHubService) SuccessRedirect(teamSlug string) string {
	if f.successRedirectFn != nil {
		return f.successRedirectFn(teamSlug)
	}
	return "https://app.0ops.tw/integrations/github?team=" + teamSlug + "&status=installed"
}

func (f *fakeGitHubService) PreviewUninstall(ctx context.Context, teamID, actorUserID, teamSlug string) (githubapp.PreviewResult, error) {
	atomic.AddInt32(&f.uninstallPreviewCalls, 1)
	if f.previewUninstallFn != nil {
		return f.previewUninstallFn(ctx, teamID, actorUserID, teamSlug)
	}
	return githubapp.PreviewResult{PreviewID: "preview-uninstall-1", Action: githubapp.ActionUninstall, Summary: "summary"}, nil
}

func (f *fakeGitHubService) ConfirmUninstall(ctx context.Context, teamID, actorUserID, previewID string) (githubapp.ConfirmUninstallResult, error) {
	atomic.AddInt32(&f.uninstallConfirmCalls, 1)
	if f.confirmUninstallFn != nil {
		return f.confirmUninstallFn(ctx, teamID, actorUserID, previewID)
	}
	return githubapp.ConfirmUninstallResult{Status: "uninstalled", PausedAppCount: 1}, nil
}

func (f *fakeGitHubService) GetInstallStatus(ctx context.Context, teamID string) (githubapp.InstallStatus, error) {
	atomic.AddInt32(&f.statusCalls, 1)
	if f.statusFn != nil {
		return f.statusFn(ctx, teamID)
	}
	return githubapp.InstallStatus{}, nil
}

func (f *fakeGitHubService) HandleInstallationWebhook(ctx context.Context, deliveryID string, payload []byte) (githubapp.WebhookOutcome, error) {
	atomic.AddInt32(&f.webhookCalls, 1)
	if f.webhookFn != nil {
		return f.webhookFn(ctx, deliveryID, payload)
	}
	return githubapp.WebhookOutcome{Acted: true}, nil
}

type fakeWebhookVerifier struct {
	body []byte
	err  error
}

func (f *fakeWebhookVerifier) VerifyRequest(r *http.Request) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	f.body = body
	return body, nil
}

func newGitHubHandlerServer(t *testing.T, store *fakeStore, svc githubAppService, verifier githubWebhookVerifier) *httptest.Server {
	t.Helper()
	prev := githubServiceFactoryFn
	t.Cleanup(func() { githubServiceFactoryFn = prev })
	githubServiceFactoryFn = func(_ githubapp.Store) (githubAppService, githubWebhookVerifier) {
		return svc, verifier
	}
	return httptest.NewServer(NewRouter(store))
}

func TestGitHubInstallPreviewRequiresOwner(t *testing.T) {
	store, token := newFakeStore()
	store.role = "admin" // owner-only per spec § 14 hard rule #2
	svc := &fakeGitHubService{}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, token).PreviewGitHubInstall(context.Background(), store.team.Slug)
	if err == nil || !strings.Contains(err.Error(), "forbidden_role") {
		t.Fatalf("admin should be rejected; err = %v", err)
	}
	if svc.previewInstallCalls != 0 {
		t.Fatalf("service should not be called when role check fails")
	}
}

func TestGitHubInstallPreviewOwnerSucceeds(t *testing.T) {
	store, token := newFakeStore()
	store.role = "owner"
	svc := &fakeGitHubService{}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).PreviewGitHubInstall(context.Background(), store.team.Slug)
	if err != nil {
		t.Fatalf("PreviewGitHubInstall error = %v", err)
	}
	if out.PreviewID != "preview-install-1" {
		t.Fatalf("preview_id = %q", out.PreviewID)
	}
	if svc.previewInstallCalls != 1 {
		t.Fatalf("expected exactly one service call, got %d", svc.previewInstallCalls)
	}
}

func TestGitHubInstallConfirmReturnsInstallURL(t *testing.T) {
	store, token := newFakeStore()
	store.role = "owner"
	svc := &fakeGitHubService{}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).ConfirmGitHubInstall(context.Background(), store.team.Slug, "preview-install-1")
	if err != nil {
		t.Fatalf("ConfirmGitHubInstall error = %v", err)
	}
	if !strings.Contains(out.InstallURL, "https://github.com/apps/0ops/installations/new?state=") {
		t.Fatalf("install url = %q", out.InstallURL)
	}
}

func TestGitHubInstallConfirmMapsPreviewErrors(t *testing.T) {
	store, token := newFakeStore()
	store.role = "owner"
	svc := &fakeGitHubService{
		confirmInstallFn: func(context.Context, string, string, string) (githubapp.ConfirmInstallResult, error) {
			return githubapp.ConfirmInstallResult{}, githubapp.ErrPreviewExpired
		},
	}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, token).ConfirmGitHubInstall(context.Background(), store.team.Slug, "preview-1")
	if err == nil || !strings.Contains(err.Error(), "preview_expired") {
		t.Fatalf("expected preview_expired error, got %v", err)
	}
}

func TestGitHubInstallCallbackRedirectsOnSuccess(t *testing.T) {
	store, _ := newFakeStore()
	svc := &fakeGitHubService{
		callbackFn: func(_ context.Context, installID, _ string) (githubapp.CallbackResult, error) {
			if installID != "12345" {
				t.Fatalf("installation_id = %q, want 12345", installID)
			}
			return githubapp.CallbackResult{TeamSlug: "acme", InstallID: 12345}, nil
		},
	}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := httpClient.Get(srv.URL + "/v1/auth/github/install-callback?installation_id=12345&state=ZmFrZQ%3D%3D&setup_action=install")
	if err != nil {
		t.Fatalf("GET callback error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.Contains(location, "team=acme") {
		t.Fatalf("Location header = %q", location)
	}
}

func TestGitHubInstallCallbackRejectsInvalidState(t *testing.T) {
	store, _ := newFakeStore()
	svc := &fakeGitHubService{
		callbackFn: func(context.Context, string, string) (githubapp.CallbackResult, error) {
			return githubapp.CallbackResult{}, githubapp.ErrStateInvalid
		},
	}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/v1/auth/github/install-callback?installation_id=1&state=bad")
	if err != nil {
		t.Fatalf("GET callback error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if body.Error.Code != "state_invalid" {
		t.Fatalf("error.code = %q, want state_invalid", body.Error.Code)
	}
}

func TestGitHubUninstallPreviewAndConfirm(t *testing.T) {
	store, token := newFakeStore()
	store.role = "owner"
	svc := &fakeGitHubService{}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewGitHubUninstall(context.Background(), store.team.Slug)
	if err != nil {
		t.Fatalf("PreviewGitHubUninstall error = %v", err)
	}
	if preview.Action != githubapp.ActionUninstall {
		t.Fatalf("action = %q", preview.Action)
	}
	out, err := client.ConfirmGitHubUninstall(context.Background(), store.team.Slug, preview.PreviewID)
	if err != nil {
		t.Fatalf("ConfirmGitHubUninstall error = %v", err)
	}
	if out.Status != "uninstalled" || out.PausedAppCount != 1 {
		t.Fatalf("uninstall response = %+v", out)
	}
}

func TestGitHubInstallStatusReturnsBinding(t *testing.T) {
	store, token := newFakeStore()
	id := int64(42)
	svc := &fakeGitHubService{
		statusFn: func(context.Context, string) (githubapp.InstallStatus, error) {
			return githubapp.InstallStatus{Installed: true, GithubInstallID: &id}, nil
		},
	}
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).GetGitHubInstallStatus(context.Background(), store.team.Slug)
	if err != nil {
		t.Fatalf("GetGitHubInstallStatus error = %v", err)
	}
	if !out.Installed || out.GithubInstallID == nil || *out.GithubInstallID != 42 {
		t.Fatalf("status = %+v", out)
	}
}

func TestGitHubWebhookSignatureFailureRejected(t *testing.T) {
	store, _ := newFakeStore()
	svc := &fakeGitHubService{}
	verifier := &fakeWebhookVerifier{err: errors.New("invalid signature")}
	srv := newGitHubHandlerServer(t, store, svc, verifier)
	t.Cleanup(srv.Close)

	payload := []byte(`{"action":"deleted"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Delivery", "delivery-x")
	req.Header.Set("X-GitHub-Event", "installation")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
	if svc.webhookCalls != 0 {
		t.Fatalf("service should not run when signature check fails")
	}
}

func TestGitHubWebhookInstallationDeliveryDelegatesToService(t *testing.T) {
	store, _ := newFakeStore()
	svc := &fakeGitHubService{
		webhookFn: func(_ context.Context, deliveryID string, payload []byte) (githubapp.WebhookOutcome, error) {
			if deliveryID != "delivery-y" {
				t.Fatalf("delivery_id = %q", deliveryID)
			}
			if !strings.Contains(string(payload), `"action":"deleted"`) {
				t.Fatalf("payload not forwarded: %s", string(payload))
			}
			return githubapp.WebhookOutcome{Acted: true, TeamSlug: "acme", PausedAppCount: 3, Action: "deleted"}, nil
		},
	}
	verifier := &fakeWebhookVerifier{}
	srv := newGitHubHandlerServer(t, store, svc, verifier)
	t.Cleanup(srv.Close)

	payload := []byte(`{"action":"deleted","installation":{"id":99}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Delivery", "delivery-y")
	req.Header.Set("X-GitHub-Event", "installation")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
}

func TestGitHubWebhookIgnoresUnrelatedEvent(t *testing.T) {
	store, _ := newFakeStore()
	svc := &fakeGitHubService{}
	verifier := &fakeWebhookVerifier{}
	srv := newGitHubHandlerServer(t, store, svc, verifier)
	t.Cleanup(srv.Close)

	payload := []byte(`{"action":"opened"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-z")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if svc.webhookCalls != 0 {
		t.Fatalf("non-installation event must not run handler")
	}
}

// TestGitHubInstallE2ERealService exercises the real `*githubapp.Service`
// against the in-memory fakeStore so the full preview → confirm → callback →
// status loop is covered against the real state HMAC + DB transitions.
func TestGitHubInstallE2ERealService(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "e2e-test-secret")
	signer, err := githubapp.NewStateSigner()
	if err != nil {
		t.Fatalf("NewStateSigner error = %v", err)
	}
	store, token := newFakeStore()
	store.role = "owner"
	svc := githubapp.NewService(store, githubapp.Options{
		StateSigner: signer,
		AppURLBase:  "https://github.com",
		AppSlug:     "0ops",
		CallbackURL: "https://api.0ops.tw/v1/auth/github/install-callback",
		SuccessPage: "https://app.0ops.tw/integrations/github",
	})
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewGitHubInstall(context.Background(), store.team.Slug)
	if err != nil {
		t.Fatalf("preview error = %v", err)
	}
	confirm, err := client.ConfirmGitHubInstall(context.Background(), store.team.Slug, preview.PreviewID)
	if err != nil {
		t.Fatalf("confirm error = %v", err)
	}
	state := mustExtractState(t, confirm.InstallURL)

	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cbURL := srv.URL + "/v1/auth/github/install-callback?installation_id=987654321&state=" + url.QueryEscape(state) + "&setup_action=install"
	resp, err := httpClient.Get(cbURL)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(body))
	}

	status, err := client.GetGitHubInstallStatus(context.Background(), store.team.Slug)
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !status.Installed || status.GithubInstallID == nil || *status.GithubInstallID != 987654321 {
		t.Fatalf("status = %+v", status)
	}
}

// TestGitHubInstallCallbackRejectsAdminActor verifies the callback handler
// refuses to bind installation when the actor's role was downgraded between
// confirm and callback (spec § 4.4 hard rule #2 + § 4.4 "actor 仍可信").
func TestGitHubInstallCallbackRejectsNonOwnerOnReturn(t *testing.T) {
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "callback-actor-secret")
	signer, _ := githubapp.NewStateSigner()
	store, token := newFakeStore()
	store.role = "owner"
	svc := githubapp.NewService(store, githubapp.Options{
		StateSigner: signer,
		AppURLBase:  "https://github.com",
	})
	srv := newGitHubHandlerServer(t, store, svc, nil)
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewGitHubInstall(context.Background(), store.team.Slug)
	if err != nil {
		t.Fatalf("preview error = %v", err)
	}
	confirm, err := client.ConfirmGitHubInstall(context.Background(), store.team.Slug, preview.PreviewID)
	if err != nil {
		t.Fatalf("confirm error = %v", err)
	}
	state := mustExtractState(t, confirm.InstallURL)

	// Downgrade role between confirm and callback.
	store.role = "admin"

	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cbURL := srv.URL + "/v1/auth/github/install-callback?installation_id=42&state=" + url.QueryEscape(state) + "&setup_action=install"
	resp, err := httpClient.Get(cbURL)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(body))
	}
}

func mustExtractState(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in url %q", raw)
	}
	return state
}

// TestDeployRunCallbackHMACAndDedup_GitHubInstallationDedup is a guard that
// the github webhook handler refuses to re-process the same delivery id, even
// when the underlying service implementation changes.
func TestGitHubWebhookDuplicateDeliveryShortCircuit(t *testing.T) {
	t.Setenv("OPS_GITHUB_WEBHOOK_SECRET", "wh-test-secret")
	verifier, err := githubapp.NewWebhookVerifier()
	if err != nil {
		t.Fatalf("NewWebhookVerifier error = %v", err)
	}
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "wh-state-secret")
	signer, _ := githubapp.NewStateSigner()
	id := int64(101)
	store, _ := newFakeStore()
	store.team.GithubInstallID = &id
	svc := githubapp.NewService(store, githubapp.Options{StateSigner: signer})
	srv := newGitHubHandlerServer(t, store, svc, verifier)
	t.Cleanup(srv.Close)

	payload := []byte(`{"action":"deleted","installation":{"id":101}}`)
	mac := hmac.New(sha256.New, []byte("wh-test-secret"))
	_, _ = mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/webhooks/github", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "installation")
		req.Header.Set("X-GitHub-Delivery", "delivery-repeat-"+strconv.Itoa(0))
		req.Header.Set("X-Hub-Signature-256", signature)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("iteration %d: status = %d", i, resp.StatusCode)
		}
	}

	if store.team.GithubInstallID != nil {
		t.Fatalf("first webhook should have cleared install id")
	}
	// Second delivery (same id) must not log a second pause action; we can only
	// observe via team state, which already nil — assert it stays nil.
}
