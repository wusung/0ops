package githubapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

type fakeStore struct {
	previews     map[string]db.Preview
	previewSeq   int
	teams        map[string]db.Team // keyed by teamID
	role         map[string]string  // teamID|userID → role
	apps         map[string]int     // teamID → app count (paused not changed by test path)
	deliveries   map[string]struct{}
	setHistory   []setCall
	pauseHistory []pauseCall
}

type setCall struct {
	TeamID    string
	Actor     string
	InstallID *int64
	Action    string
}

type pauseCall struct {
	TeamID string
}

type fakeAPI struct {
	deleted  []int64
	deleteFn func(int64) error
}

func (f *fakeAPI) DeleteInstallation(_ context.Context, installID int64) error {
	f.deleted = append(f.deleted, installID)
	if f.deleteFn != nil {
		return f.deleteFn(installID)
	}
	return nil
}

type fakeCache struct {
	invalidated []int64
}

func (f *fakeCache) Invalidate(installID int64) {
	f.invalidated = append(f.invalidated, installID)
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		previews:   map[string]db.Preview{},
		teams:      map[string]db.Team{},
		role:       map[string]string{},
		apps:       map[string]int{},
		deliveries: map[string]struct{}{},
	}
}

func (f *fakeStore) CreatePreview(_ context.Context, teamID, actorUserID, action string, args json.RawMessage, _ string) (db.Preview, error) {
	f.previewSeq++
	now := time.Now().UTC()
	p := db.Preview{
		ID:          "preview-" + actionToShort(action) + "-" + itoa(f.previewSeq),
		TeamID:      teamID,
		ActorUserID: actorUserID,
		Action:      action,
		Args:        append(json.RawMessage(nil), args...),
		CreatedAt:   now,
		ExpiresAt:   now.Add(10 * time.Minute),
	}
	f.previews[p.ID] = p
	return p, nil
}

func (f *fakeStore) GetPreview(_ context.Context, previewID string) (db.Preview, error) {
	p, ok := f.previews[previewID]
	if !ok {
		return db.Preview{}, db.ErrPreviewNotFound
	}
	return p, nil
}

func (f *fakeStore) ConsumePreviewWithResult(_ context.Context, previewID string, result json.RawMessage) error {
	p, ok := f.previews[previewID]
	if !ok {
		return db.ErrPreviewNotFound
	}
	if p.ConsumedAt != nil {
		return db.ErrPreviewConsumed
	}
	now := time.Now().UTC()
	p.ConsumedAt = &now
	p.LastResult = append(json.RawMessage(nil), result...)
	f.previews[previewID] = p
	return nil
}

func (f *fakeStore) GetTeamByID(_ context.Context, teamID string) (db.Team, error) {
	team, ok := f.teams[teamID]
	if !ok {
		return db.Team{}, db.ErrTeamNotFound
	}
	return team, nil
}

func (f *fakeStore) FindTeamByGitHubInstallID(_ context.Context, installID int64) (db.Team, error) {
	for _, t := range f.teams {
		if t.GithubInstallID != nil && *t.GithubInstallID == installID {
			return t, nil
		}
	}
	return db.Team{}, db.ErrTeamNotFound
}

func (f *fakeStore) SetTeamGitHubInstall(_ context.Context, teamID, actorUserID string, installID *int64, action string, _ map[string]any, _ map[string]any) error {
	team, ok := f.teams[teamID]
	if !ok {
		return db.ErrTeamNotFound
	}
	team.GithubInstallID = copyInstallID(installID)
	f.teams[teamID] = team
	f.setHistory = append(f.setHistory, setCall{TeamID: teamID, Actor: actorUserID, InstallID: copyInstallID(installID), Action: action})
	return nil
}

func (f *fakeStore) PauseTeamApps(_ context.Context, teamID string) (int64, error) {
	f.pauseHistory = append(f.pauseHistory, pauseCall{TeamID: teamID})
	return int64(f.apps[teamID]), nil
}

func (f *fakeStore) GetTeamMembershipRole(_ context.Context, teamID, userID string) (string, error) {
	if role, ok := f.role[teamID+"|"+userID]; ok {
		return role, nil
	}
	return "", errors.New("not found")
}

func (f *fakeStore) RegisterWebhookDelivery(_ context.Context, provider, deliveryID string) (bool, error) {
	key := provider + "::" + deliveryID
	if _, ok := f.deliveries[key]; ok {
		return false, nil
	}
	f.deliveries[key] = struct{}{}
	return true, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}

func actionToShort(action string) string {
	switch action {
	case ActionInstall:
		return "install"
	case ActionUninstall:
		return "uninstall"
	}
	return "preview"
}

func newTestService(t *testing.T) (*Service, *fakeStore, *fakeAPI, *fakeCache) {
	t.Helper()
	t.Setenv("OPS_GITHUB_APP_STATE_HMAC_SECRET", "unit-test-secret")
	signer, err := NewStateSigner()
	if err != nil {
		t.Fatalf("NewStateSigner() error = %v", err)
	}
	store := newFakeStore()
	api := &fakeAPI{}
	cache := &fakeCache{}
	svc := NewService(store, Options{
		StateSigner: signer,
		APIClient:   api,
		TokenCache:  cache,
		AppURLBase:  "https://github.com",
		AppSlug:     "0ops",
		CallbackURL: "https://api.0ops.tw/v1/auth/github/install-callback",
		SuccessPage: "https://app.0ops.tw/integrations/github",
	})
	return svc, store, api, cache
}

func seedTeam(store *fakeStore, teamID, slug string, installID *int64) {
	store.teams[teamID] = db.Team{ID: teamID, Slug: slug, Name: slug, Plan: "free", GithubInstallID: copyInstallID(installID)}
}

func TestPreviewInstallReturnsPreviewRow(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)

	out, err := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	if err != nil {
		t.Fatalf("PreviewInstall error = %v", err)
	}
	if out.PreviewID == "" || out.Action != ActionInstall {
		t.Fatalf("preview = %+v", out)
	}
	if !strings.Contains(out.Summary, "acme") {
		t.Fatalf("summary missing team slug: %q", out.Summary)
	}
}

func TestConfirmInstallProducesInstallURLAndPersists(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)

	preview, err := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	if err != nil {
		t.Fatalf("PreviewInstall error = %v", err)
	}

	res, err := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("ConfirmInstall error = %v", err)
	}
	if !strings.Contains(res.InstallURL, "https://github.com/apps/0ops/installations/new?state=") {
		t.Fatalf("install url = %q", res.InstallURL)
	}
	row := store.previews[preview.PreviewID]
	if row.ConsumedAt == nil {
		t.Fatal("expected preview to be marked consumed")
	}
	if len(row.LastResult) == 0 {
		t.Fatal("expected last_result to be persisted")
	}
}

func TestConfirmInstallReplaysLastResult(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	first, err := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("first confirm error = %v", err)
	}
	second, err := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("replay confirm error = %v", err)
	}
	if !second.Replayed {
		t.Fatal("expected Replayed=true on second confirm")
	}
	if second.InstallURL != first.InstallURL {
		t.Fatalf("replay url mismatch: first=%q second=%q", first.InstallURL, second.InstallURL)
	}
}

func TestConfirmInstallCrossActorPreviewDenied(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")

	_, err := svc.ConfirmInstall(context.Background(), "team-1", "user-2", preview.PreviewID)
	if !errors.Is(err, ErrPreviewNotFound) {
		t.Fatalf("ConfirmInstall cross-actor error = %v, want %v", err, ErrPreviewNotFound)
	}
}

func TestConfirmInstallExpiredPreview(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	row := store.previews[preview.PreviewID]
	row.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	store.previews[preview.PreviewID] = row

	_, err := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("ConfirmInstall expired error = %v, want %v", err, ErrPreviewExpired)
	}
}

func TestHandleCallbackBindsInstallationAndRequiresOwner(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	store.role["team-1|user-1"] = "owner"

	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	confirm, err := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("ConfirmInstall error = %v", err)
	}
	state := extractStateFromURL(t, confirm.InstallURL)

	result, err := svc.HandleCallback(context.Background(), "987654321", state)
	if err != nil {
		t.Fatalf("HandleCallback error = %v", err)
	}
	if result.InstallID != 987654321 {
		t.Fatalf("install_id = %d", result.InstallID)
	}
	if result.TeamSlug != "acme" {
		t.Fatalf("team_slug = %q", result.TeamSlug)
	}
	if store.teams["team-1"].GithubInstallID == nil || *store.teams["team-1"].GithubInstallID != 987654321 {
		t.Fatalf("team install id not set: %+v", store.teams["team-1"].GithubInstallID)
	}
	if len(store.setHistory) != 1 || store.setHistory[0].Action != "github_app_install_callback" {
		t.Fatalf("set history = %+v", store.setHistory)
	}
}

func TestHandleCallbackRejectsNonOwner(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	store.role["team-1|user-1"] = "admin"

	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	confirm, _ := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	state := extractStateFromURL(t, confirm.InstallURL)

	_, err := svc.HandleCallback(context.Background(), "555", state)
	if !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("HandleCallback admin error = %v, want %v", err, ErrStateInvalid)
	}
}

func TestHandleCallbackRejectsTamperedState(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	store.role["team-1|user-1"] = "owner"

	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	confirm, _ := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	state := extractStateFromURL(t, confirm.InstallURL)
	if len(state) < 5 {
		t.Fatalf("state too short: %q", state)
	}
	tampered := state[:len(state)-2] + flipChar(state[len(state)-2:])

	_, err := svc.HandleCallback(context.Background(), "555", tampered)
	if !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("HandleCallback tampered = %v, want %v", err, ErrStateInvalid)
	}
}

func TestHandleCallbackRejectsPreInstallStatePreviewNotConsumed(t *testing.T) {
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	store.role["team-1|user-1"] = "owner"

	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	// Sign state manually without consuming preview to simulate forgery.
	state, err := svc.stateSigner.SignState("team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("SignState error = %v", err)
	}
	_, err = svc.HandleCallback(context.Background(), "555", state)
	if !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("HandleCallback pre-confirm = %v, want %v", err, ErrStateInvalid)
	}
}

func TestHandleCallbackReplaceMarkedReplaced(t *testing.T) {
	existing := int64(111)
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	store.role["team-1|user-1"] = "owner"

	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	confirm, _ := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	state := extractStateFromURL(t, confirm.InstallURL)

	result, err := svc.HandleCallback(context.Background(), "222", state)
	if err != nil {
		t.Fatalf("HandleCallback error = %v", err)
	}
	if !result.Replaced {
		t.Fatal("expected Replaced=true on reinstall")
	}
}

func TestConfirmUninstallCallsGitHubAndPausesApps(t *testing.T) {
	existing := int64(42)
	svc, store, api, cache := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	store.apps["team-1"] = 3

	preview, _ := svc.PreviewUninstall(context.Background(), "team-1", "user-1", "acme")
	result, err := svc.ConfirmUninstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("ConfirmUninstall error = %v", err)
	}
	if result.Status != "uninstalled" {
		t.Fatalf("status = %q, want uninstalled", result.Status)
	}
	if result.PausedAppCount != 3 {
		t.Fatalf("paused = %d, want 3", result.PausedAppCount)
	}
	if len(api.deleted) != 1 || api.deleted[0] != 42 {
		t.Fatalf("github delete = %+v, want [42]", api.deleted)
	}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != 42 {
		t.Fatalf("cache invalidate = %+v, want [42]", cache.invalidated)
	}
	if store.teams["team-1"].GithubInstallID != nil {
		t.Fatalf("team install id should be cleared: %+v", store.teams["team-1"].GithubInstallID)
	}
}

func TestConfirmUninstallReplaysWhenAlreadyConsumed(t *testing.T) {
	existing := int64(42)
	svc, store, api, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	store.apps["team-1"] = 1

	preview, _ := svc.PreviewUninstall(context.Background(), "team-1", "user-1", "acme")
	first, err := svc.ConfirmUninstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("first error = %v", err)
	}
	second, err := svc.ConfirmUninstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("replay error = %v", err)
	}
	if !second.Replayed {
		t.Fatal("expected Replayed=true on retry")
	}
	if second.PausedAppCount != first.PausedAppCount {
		t.Fatalf("replay mismatch: first=%+v second=%+v", first, second)
	}
	if len(api.deleted) != 1 {
		t.Fatalf("expected exactly one github DELETE call, got %d", len(api.deleted))
	}
}

func TestConfirmUninstallTeamWithNoInstallReturnsNoInstall(t *testing.T) {
	svc, store, api, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", nil)
	preview, _ := svc.PreviewUninstall(context.Background(), "team-1", "user-1", "acme")
	result, err := svc.ConfirmUninstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err != nil {
		t.Fatalf("ConfirmUninstall error = %v", err)
	}
	if result.Status != "no_install" {
		t.Fatalf("status = %q, want no_install", result.Status)
	}
	if len(api.deleted) != 0 {
		t.Fatalf("expected no github DELETE, got %+v", api.deleted)
	}
}

func TestConfirmUninstallGitHubFailureKeepsBinding(t *testing.T) {
	existing := int64(99)
	svc, store, api, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	api.deleteFn = func(int64) error { return errors.New("rate limited") }

	preview, _ := svc.PreviewUninstall(context.Background(), "team-1", "user-1", "acme")
	_, err := svc.ConfirmUninstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if err == nil {
		t.Fatal("expected error when github delete fails")
	}
	if store.teams["team-1"].GithubInstallID == nil {
		t.Fatal("install id should not be cleared when github delete fails")
	}
}

func TestGetInstallStatusReportsBinding(t *testing.T) {
	id := int64(7)
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &id)

	st, err := svc.GetInstallStatus(context.Background(), "team-1")
	if err != nil {
		t.Fatalf("GetInstallStatus error = %v", err)
	}
	if !st.Installed || st.GithubInstallID == nil || *st.GithubInstallID != 7 {
		t.Fatalf("status = %+v", st)
	}
}

func TestHandleWebhookDeletedUnbindsTeamAndPausesApps(t *testing.T) {
	existing := int64(101)
	svc, store, _, cache := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	store.apps["team-1"] = 2

	payload := []byte(`{"action":"deleted","installation":{"id":101}}`)
	outcome, err := svc.HandleInstallationWebhook(context.Background(), "delivery-x", payload)
	if err != nil {
		t.Fatalf("HandleInstallationWebhook error = %v", err)
	}
	if !outcome.Acted || outcome.PausedAppCount != 2 || outcome.TeamSlug != "acme" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if store.teams["team-1"].GithubInstallID != nil {
		t.Fatal("install id should be cleared after webhook deleted")
	}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != 101 {
		t.Fatalf("cache invalidate = %+v", cache.invalidated)
	}
}

func TestHandleWebhookSuspendUnbindsTeam(t *testing.T) {
	existing := int64(202)
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	store.apps["team-1"] = 1

	payload := []byte(`{"action":"suspend","installation":{"id":202}}`)
	outcome, err := svc.HandleInstallationWebhook(context.Background(), "delivery-y", payload)
	if err != nil {
		t.Fatalf("HandleInstallationWebhook error = %v", err)
	}
	if !outcome.Acted || outcome.PausedAppCount != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if store.teams["team-1"].GithubInstallID != nil {
		t.Fatal("install id should be cleared on suspend")
	}
}

func TestHandleWebhookDuplicateDeliveryNoOp(t *testing.T) {
	existing := int64(303)
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)
	store.apps["team-1"] = 1

	payload := []byte(`{"action":"deleted","installation":{"id":303}}`)
	first, err := svc.HandleInstallationWebhook(context.Background(), "dup-1", payload)
	if err != nil || !first.Acted {
		t.Fatalf("first = %+v, err = %v", first, err)
	}
	// Restore the binding to detect a double-execution.
	team := store.teams["team-1"]
	idCopy := existing
	team.GithubInstallID = &idCopy
	store.teams["team-1"] = team

	second, err := svc.HandleInstallationWebhook(context.Background(), "dup-1", payload)
	if err != nil {
		t.Fatalf("duplicate error = %v", err)
	}
	if !second.Duplicate || second.Acted {
		t.Fatalf("duplicate outcome = %+v", second)
	}
	if store.teams["team-1"].GithubInstallID == nil {
		t.Fatal("duplicate webhook should not re-unbind team")
	}
}

func TestHandleWebhookUnknownActionNoOp(t *testing.T) {
	existing := int64(404)
	svc, store, _, _ := newTestService(t)
	seedTeam(store, "team-1", "acme", &existing)

	payload := []byte(`{"action":"new_permissions_accepted","installation":{"id":404}}`)
	outcome, err := svc.HandleInstallationWebhook(context.Background(), "delivery-z", payload)
	if err != nil {
		t.Fatalf("HandleInstallationWebhook error = %v", err)
	}
	if outcome.Acted {
		t.Fatalf("new_permissions_accepted should not act: %+v", outcome)
	}
	if store.teams["team-1"].GithubInstallID == nil {
		t.Fatal("install id should be preserved")
	}
}

func TestPreviewInstallEmptyTeamID(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.PreviewInstall(context.Background(), "", "user-1", "acme"); !errors.Is(err, ErrTeamMissing) {
		t.Fatalf("expected ErrTeamMissing, got %v", err)
	}
	if _, err := svc.PreviewInstall(context.Background(), "team-1", "", "acme"); !errors.Is(err, ErrActorMissing) {
		t.Fatalf("expected ErrActorMissing, got %v", err)
	}
}

func TestConfirmInstallSignerMissingErr(t *testing.T) {
	store := newFakeStore()
	seedTeam(store, "team-1", "acme", nil)
	svc := NewService(store, Options{AppURLBase: "https://github.com"})

	preview, _ := svc.PreviewInstall(context.Background(), "team-1", "user-1", "acme")
	_, err := svc.ConfirmInstall(context.Background(), "team-1", "user-1", preview.PreviewID)
	if !errors.Is(err, ErrSignerMissing) {
		t.Fatalf("ConfirmInstall without signer = %v, want %v", err, ErrSignerMissing)
	}
}

func extractStateFromURL(t *testing.T, installURL string) string {
	t.Helper()
	parsed, err := url.Parse(installURL)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", installURL, err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("install url has no state=: %q", installURL)
	}
	return state
}

func flipChar(s string) string {
	out := []byte(s)
	if len(out) == 0 {
		return s
	}
	out[0] ^= 0x01
	return string(out)
}
