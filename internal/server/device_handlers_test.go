package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/winshare/zeroops/internal/server/auth"
)

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
	handler := authorizeToolsHandler()

	reqBody := AuthorizeToolsRequest{Tools: []string{"nonexistent_tool"}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/teams/team-1/auth:grant-tools", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthorizeToolsValid(t *testing.T) {
	handler := authorizeToolsHandler()

	reqBody := AuthorizeToolsRequest{Tools: []string{"list_apps", "get_app"}}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/teams/team-1/auth:grant-tools", bytes.NewReader(body))
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
}
