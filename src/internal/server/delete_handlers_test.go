package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// helper: seed a fakeStore that's RBAC-ready for delete_app — admin role
// plus the apps:delete scope. The default newFakeStore() token does NOT
// carry apps:delete (spec § 13 hard rule #2 is enforced by the middleware
// scope check); we extend it here so the path through to the handler
// reaches the service layer.
func deleteAppCapableStore(t *testing.T) (*fakeStore, string) {
	t.Helper()
	store, token := newFakeStore()
	store.role = "admin"
	store.token.Scopes = []string{"apps:read", "apps:write", "apps:delete", "teams:read", "members:manage"}
	store.tokens[store.token.ID] = store.token
	return store, token
}

func TestPreviewDeleteAppRejectsConfirmMismatch(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, token).PreviewDeleteApp(context.Background(), store.team.Slug, "alpha", dto.AppDeleteRequest{Confirm: "wrong"})
	if err == nil {
		t.Fatalf("expected error on confirm mismatch")
	}
	if !strings.Contains(err.Error(), "confirm_mismatch") {
		t.Fatalf("error = %v, want confirm_mismatch", err)
	}
}

func TestPreviewDeleteAppRejectsMissingApp(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)
	_, err := backendclient.New(srv.URL, token).PreviewDeleteApp(context.Background(), store.team.Slug, "ghost", dto.AppDeleteRequest{Confirm: "ghost"})
	if err == nil {
		t.Fatalf("expected app_not_found error")
	}
	if !strings.Contains(err.Error(), "app_not_found") {
		t.Fatalf("error = %v, want app_not_found", err)
	}
}

func TestPreviewDeleteAppReturnsSideEffects(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).PreviewDeleteApp(context.Background(), store.team.Slug, "alpha", dto.AppDeleteRequest{Confirm: "alpha"})
	if err != nil {
		t.Fatalf("PreviewDeleteApp() error = %v", err)
	}
	if out.PreviewID == "" {
		t.Fatalf("PreviewID missing")
	}
	if out.Action != "delete_app" {
		t.Fatalf("Action = %q, want delete_app", out.Action)
	}
	if len(out.SideEffects) != 5 {
		t.Fatalf("expected 5 side_effects, got %d", len(out.SideEffects))
	}
	if out.SideEffects[4].Reversible {
		t.Fatalf("argocd_prune_resources must be irreversible")
	}
}

func TestDeleteAppRequiresAppsDeleteScope(t *testing.T) {
	store, token := newFakeStore() // default token lacks apps:delete
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	resp, err := postJSON(t, srv.URL+"/v1/teams/"+store.team.Slug+"/apps/alpha:preview-delete", token, map[string]any{"confirm": "alpha"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestDeleteAppRequiresAdminRole(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	store.role = "member"
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	resp, err := postJSON(t, srv.URL+"/v1/teams/"+store.team.Slug+"/apps/alpha:preview-delete", token, map[string]any{"confirm": "alpha"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member role status = %d, want 403", resp.StatusCode)
	}
}

func TestDeleteAppFullFlowConfirmEnqueuesResidue(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	store.apps[0].Status = strPtr("live")
	store.domainBindings = []db.DomainBinding{
		{ID: "db-primary", TeamID: store.team.ID, AppID: "1", AppSlug: "alpha", Hostname: "alpha.jesontech.com", Kind: strPtr("primary")},
	}
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewDeleteApp(context.Background(), store.team.Slug, "alpha", dto.AppDeleteRequest{Confirm: "alpha"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	resp, err := client.DeleteApp(context.Background(), store.team.Slug, "alpha", dto.ConfirmDeleteAppRequest{PreviewID: preview.PreviewID, ConfirmationPhrase: preview.RequiredPhrase})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.AppSlug != "alpha" {
		t.Fatalf("AppSlug = %q, want alpha", resp.AppSlug)
	}
	if len(store.reconciliationJobs) != 1 {
		t.Fatalf("expected 1 cleanup_residue job, got %d", len(store.reconciliationJobs))
	}
	if got := *store.apps[0].Status; got != "deleting" {
		t.Fatalf("app status = %q, want deleting", got)
	}

	// Replay must return idempotently.
	resp2, err := client.DeleteApp(context.Background(), store.team.Slug, "alpha", dto.ConfirmDeleteAppRequest{PreviewID: preview.PreviewID, ConfirmationPhrase: preview.RequiredPhrase})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if resp2.AppID != resp.AppID {
		t.Fatalf("replay returned different app id")
	}
}

// spec § 11: a delete_app preview reports risk_level=critical and a
// backend-generated required_phrase.
func TestPreviewDeleteAppReportsCriticalRisk(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	out, err := backendclient.New(srv.URL, token).PreviewDeleteApp(context.Background(), store.team.Slug, "alpha", dto.AppDeleteRequest{Confirm: "alpha"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if out.RiskLevel != "critical" {
		t.Fatalf("RiskLevel = %q, want critical", out.RiskLevel)
	}
	if out.RequiredPhrase != "DELETE alpha" {
		t.Fatalf("RequiredPhrase = %q, want %q", out.RequiredPhrase, "DELETE alpha")
	}
}

// spec § 11: confirming a high-risk action without confirmation_phrase is
// rejected with a 400 confirmation_phrase_mismatch envelope.
func TestDeleteAppRejectsMissingConfirmationPhrase(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	store.apps[0].Status = strPtr("live")
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	preview, err := client.PreviewDeleteApp(context.Background(), store.team.Slug, "alpha", dto.AppDeleteRequest{Confirm: "alpha"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	_, err = client.DeleteApp(context.Background(), store.team.Slug, "alpha", dto.ConfirmDeleteAppRequest{PreviewID: preview.PreviewID})
	if err == nil || !strings.Contains(err.Error(), "confirmation_phrase_mismatch") {
		t.Fatalf("error = %v, want confirmation_phrase_mismatch", err)
	}
	if len(store.reconciliationJobs) != 0 {
		t.Fatalf("delete must not execute without confirmation_phrase")
	}
}

// spec § 11: a correct confirmation_phrase against a forged/expired
// preview_id is still rejected — phrase does not replace preview_id.
func TestDeleteAppPhraseDoesNotReplacePreviewID(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	store.apps[0].Status = strPtr("live")
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, token).DeleteApp(context.Background(), store.team.Slug, "alpha", dto.ConfirmDeleteAppRequest{PreviewID: "11111111-1111-1111-1111-111111111111", ConfirmationPhrase: "DELETE alpha"})
	if err == nil || !strings.Contains(err.Error(), "preview_not_found") {
		t.Fatalf("error = %v, want preview_not_found", err)
	}
}

func TestDeleteAppRejectsAlreadyDeleting(t *testing.T) {
	store, token := deleteAppCapableStore(t)
	store.apps[0].Status = strPtr("deleting")
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	_, err := backendclient.New(srv.URL, token).PreviewDeleteApp(context.Background(), store.team.Slug, "alpha", dto.AppDeleteRequest{Confirm: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "already_deleting") {
		t.Fatalf("error = %v, want already_deleting", err)
	}
}

// postJSON is a minimal helper used to bypass the typed client and hit
// the raw endpoint when we need to assert on HTTP status codes such as
// 403 forbidden_role / forbidden_scope.
func postJSON(t *testing.T, url, token string, body map[string]any) (*http.Response, error) {
	t.Helper()
	encoded := mustMarshalJSON(t, body)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

func mustMarshalJSON(t *testing.T, v any) string {
	t.Helper()
	switch x := v.(type) {
	case string:
		return x
	default:
		// Manual encoding to avoid adding json import here when the value is
		// a tiny map.
		var sb strings.Builder
		sb.WriteRune('{')
		first := true
		for k, val := range v.(map[string]any) {
			if !first {
				sb.WriteRune(',')
			}
			first = false
			sb.WriteRune('"')
			sb.WriteString(k)
			sb.WriteString(`":"`)
			sb.WriteString(val.(string))
			sb.WriteRune('"')
		}
		sb.WriteRune('}')
		return sb.String()
	}
}
