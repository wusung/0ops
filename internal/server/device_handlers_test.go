package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
)

type mockToolGrantsStore struct {
	grants map[string]map[string]bool
}

func newMockToolGrantsStore() *mockToolGrantsStore {
	return &mockToolGrantsStore{
		grants: make(map[string]map[string]bool),
	}
}

func (m *mockToolGrantsStore) IsToolGranted(ctx context.Context, teamID, userID, toolID string) (bool, error) {
	key := teamID + ":" + userID
	if tools, ok := m.grants[key]; ok {
		return tools[toolID], nil
	}
	return false, nil
}

func (m *mockToolGrantsStore) ListGrantedTools(ctx context.Context, teamID, userID string) ([]string, error) {
	key := teamID + ":" + userID
	var tools []string
	if toolMap, ok := m.grants[key]; ok {
		for tool, allowed := range toolMap {
			if allowed {
				tools = append(tools, tool)
			}
		}
	}
	return tools, nil
}

func (m *mockToolGrantsStore) UpsertToolGrant(ctx context.Context, teamID, userID, toolID string, allowed bool, grantedByActorID *string) error {
	key := teamID + ":" + userID
	if _, ok := m.grants[key]; !ok {
		m.grants[key] = make(map[string]bool)
	}
	m.grants[key][toolID] = allowed
	return nil
}

func (m *mockToolGrantsStore) RevokeToolGrant(ctx context.Context, teamID, userID, toolID string) error {
	key := teamID + ":" + userID
	if tools, ok := m.grants[key]; ok {
		delete(tools, toolID)
	}
	return nil
}

func (m *mockToolGrantsStore) ListAllUserGrants(ctx context.Context, teamID, userID string) ([]db.ToolGrant, error) {
	key := teamID + ":" + userID
	var grants []db.ToolGrant
	if toolMap, ok := m.grants[key]; ok {
		for tool, allowed := range toolMap {
			grants = append(grants, db.ToolGrant{
				ToolID:  tool,
				Allowed: allowed,
			})
		}
	}
	return grants, nil
}

func TestDeviceFlowStart(t *testing.T) {
	handler := deviceFlowStartHandler()

	req := httptest.NewRequest("POST", "/v1/auth/device/start", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp auth.DeviceFlowResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.UserCode == "" {
		t.Error("user_code should not be empty")
	}

	if resp.DeviceCode == "" {
		t.Error("device_code should not be empty")
	}

	if resp.VerificationURI != "https://github.com/login/device" {
		t.Errorf("verification_uri should be GitHub device URL, got %s", resp.VerificationURI)
	}
}

func TestDeviceFlowPollMissingToken(t *testing.T) {
	handler := deviceFlowPollHandler()

	req := httptest.NewRequest("POST", "/v1/auth/device/poll", bytes.NewReader([]byte(`{"poll_token":""}`)))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDeviceFlowPoll(t *testing.T) {
	handler := deviceFlowPollHandler()

	reqBody := DeviceFlowPollRequest{PollToken: "valid_poll_token"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/auth/device/poll", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp auth.DevicePollResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.AccessToken == "" {
		t.Error("access_token should not be empty")
	}

	if resp.Team.ID == "" {
		t.Error("team.id should not be empty")
	}

	if len(resp.AvailableTools) == 0 {
		t.Error("available_tools should not be empty")
	}

	if resp.NextStep != "grant_tools" {
		t.Errorf("next_step should be grant_tools, got %s", resp.NextStep)
	}
}

func TestAuthorizeToolsInvalidTool(t *testing.T) {
	store := newMockToolGrantsStore()
	handler := authorizeToolsHandler(store)

	reqBody := AuthorizeToolsRequest{Tools: []string{"nonexistent_tool"}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/teams/team-1/auth:grant-tools", bytes.NewReader(body))

	// Set up chi router context for team_slug
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("team_slug", "team-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthorizeToolsValid(t *testing.T) {
	store := newMockToolGrantsStore()
	handler := authorizeToolsHandler(store)

	reqBody := AuthorizeToolsRequest{Tools: []string{"list_apps", "get_app"}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/teams/team-1/auth:grant-tools", bytes.NewReader(body))

	// Set up chi router context for team_slug
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("team_slug", "team-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("access_token should not be empty")
	}

	if resp["auth_status"] != "authorized" {
		t.Errorf("auth_status should be authorized, got %v", resp["auth_status"])
	}

	grantedTools, ok := resp["granted_tools"].([]interface{})
	if !ok {
		t.Error("granted_tools should be an array")
		return
	}

	if len(grantedTools) != 2 {
		t.Errorf("expected 2 granted tools, got %d", len(grantedTools))
	}
}
