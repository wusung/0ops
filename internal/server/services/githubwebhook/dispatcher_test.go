package githubwebhook_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/githubapp"
	"github.com/winshare/zeroops/internal/server/services/githubwebhook"
)

type fakeDispatcherStore struct {
	mu         sync.Mutex
	deliveries map[string]bool
}

func newFakeDispatcherStore() *fakeDispatcherStore {
	return &fakeDispatcherStore{deliveries: map[string]bool{}}
}

func (f *fakeDispatcherStore) RegisterWebhookDelivery(_ context.Context, provider, deliveryID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := provider + "::" + deliveryID
	if f.deliveries[key] {
		return false, nil
	}
	f.deliveries[key] = true
	return true, nil
}

// PushHandlerStore stub methods to satisfy the dispatchStore interface used
// by the push handler — for dispatcher-level tests we don't exercise these.
func (f *fakeDispatcherStore) FindTeamByGitHubInstallID(_ context.Context, _ int64) (db.Team, error) {
	return db.Team{}, db.ErrTeamNotFound
}
func (f *fakeDispatcherStore) FindLiveAppsByRepoAndBranch(_ context.Context, _, _, _ string) ([]db.App, error) {
	return nil, nil
}
func (f *fakeDispatcherStore) HasInFlightDeployRun(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeDispatcherStore) InsertRedeployRun(_ context.Context, _ db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error) {
	return db.InsertRedeployRunResult{}, nil
}
func (f *fakeDispatcherStore) AppendWebhookAudit(_ context.Context, _ string, _ string, _, _ map[string]any) error {
	return nil
}

type fakeVerifier struct {
	body []byte
	err  error
}

func (f *fakeVerifier) VerifyRequest(r *http.Request) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Drain body so a real-world flow that calls io.ReadAll behaves the
	// same. Caller may have wrapped Body with our bounded reader already.
	body := f.body
	if body == nil {
		buf := bytes.Buffer{}
		_, _ = buf.ReadFrom(r.Body)
		body = buf.Bytes()
	}
	return body, nil
}

type fakeInstallation struct {
	calls int
	out   githubapp.WebhookOutcome
}

func (f *fakeInstallation) HandleInstallationWebhook(_ context.Context, _ string, _ []byte) (githubapp.WebhookOutcome, error) {
	f.calls++
	return f.out, nil
}

func newSignedRequest(t *testing.T, secret string, body []byte, headers map[string]string) *http.Request {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signed := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signed)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestDispatcherSignatureFailure(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{err: errors.New("bad sig")}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), &fakeInstallation{})

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader([]byte(`{"ref":"refs/heads/main"}`)))
	req.Header.Set("X-GitHub-Event", "push")
	_, err := d.Dispatch(context.Background(), req)
	var sigErr githubwebhook.ErrSignatureInvalid
	if !errors.As(err, &sigErr) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestDispatcherUnsupportedEventReturnsIgnored(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte("{}")}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), &fakeInstallation{})

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "pull_request")
	res, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Status != "ignored_event" {
		t.Fatalf("status = %q, want ignored_event", res.Status)
	}
}

func TestDispatcherPingResponds(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte("{}")}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), &fakeInstallation{})

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "ping")
	res, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Status != "pong" {
		t.Fatalf("status = %q, want pong", res.Status)
	}
}

func TestDispatcherPushEventRoutesAndDedups(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte(`{"ref":"refs/heads/main","after":"abc","installation":{"id":1},"repository":{"html_url":"https://github.com/foo/bar","default_branch":"main"}}`)}
	pushStore := &fakeStore{
		team:     &db.Team{ID: "team-1", Slug: "acme"},
		apps:     []db.App{newLiveApp("alpha", "https://github.com/foo/bar", "main")},
		inFlight: map[string]bool{},
	}
	trigger := &fakeTrigger{}
	pushHandler := githubwebhook.NewPushHandler(pushStore, trigger)
	d := githubwebhook.NewDispatcher(store, verifier, pushHandler, &fakeInstallation{})

	for i, want := range []string{"triggered", "duplicate"} {
		body := []byte("{}")
		req := newSignedRequest(t, "secret", body, map[string]string{
			"X-GitHub-Event":    "push",
			"X-GitHub-Delivery": "delivery-push-1",
		})
		res, err := d.Dispatch(context.Background(), req)
		if err != nil {
			t.Fatalf("iteration %d err = %v", i, err)
		}
		if res.Status != want {
			t.Fatalf("iteration %d status = %q, want %q", i, res.Status, want)
		}
	}
	if len(trigger.calls) != 1 {
		t.Fatalf("trigger called %d times, want 1 (replay should dedup)", len(trigger.calls))
	}
}

func TestDispatcherPushEventMissingDeliveryID(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte("{}")}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), &fakeInstallation{})

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "push")
	_, err := d.Dispatch(context.Background(), req)
	var missing githubwebhook.ErrMissingDeliveryID
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want ErrMissingDeliveryID", err)
	}
}

func TestDispatcherPayloadTooLarge(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte("{}")}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), &fakeInstallation{})

	body := bytes.Repeat([]byte("a"), githubwebhook.MaxPayloadBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	_, err := d.Dispatch(context.Background(), req)
	var tooLarge githubwebhook.ErrPayloadTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestDispatcherInstallationDelegated(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte(`{"action":"deleted"}`)}
	inst := &fakeInstallation{out: githubapp.WebhookOutcome{Acted: true, TeamSlug: "acme"}}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), inst)

	req := newSignedRequest(t, "secret", []byte("{}"), map[string]string{
		"X-GitHub-Event":    "installation",
		"X-GitHub-Delivery": "install-1",
	})
	res, err := d.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if inst.calls != 1 || res.Status != "applied" || res.Installation.TeamSlug != "acme" {
		t.Fatalf("dispatch outcome = %+v, calls=%d", res, inst.calls)
	}
}

// Ensure DispatchResult.Status carries through stable numeric content so
// downstream metrics labels stay stable across releases.
func TestDispatcherDeliveryIDPropagatesToResult(t *testing.T) {
	store := newFakeDispatcherStore()
	verifier := &fakeVerifier{body: []byte("{}")}
	d := githubwebhook.NewDispatcher(store, verifier, githubwebhook.NewPushHandler(store, &fakeTrigger{}), &fakeInstallation{})

	for i := 0; i < 2; i++ {
		req := newSignedRequest(t, "secret", []byte("{}"), map[string]string{
			"X-GitHub-Event":    "ping",
			"X-GitHub-Delivery": "ping-" + strconv.Itoa(i),
		})
		_, err := d.Dispatch(context.Background(), req)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	}
}
