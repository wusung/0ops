package server

import (
	"encoding/json"
	"net/http"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
)

// DeviceFlowStartRequest is the request body for starting device flow
type DeviceFlowStartRequest struct {
	// Optional scopes override; if not provided, uses default
	Scopes string `json:"scopes,omitempty"`
}

// DeviceFlowPollRequest is the request body for polling authorization
type DeviceFlowPollRequest struct {
	PollToken string `json:"poll_token"`
}

func deviceFlowStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DeviceFlowStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request body", nil)
			return
		}

		// TODO: Implement actual GitHub device flow start
		// For now, return a placeholder response
		resp := auth.DeviceFlowResponse{
			DeviceCode:      "placeholder_device_code",
			UserCode:        "ABC-1234",
			VerificationURI: "https://github.com/login/device",
			ExpiresIn:       900,
			Interval:        5,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

func deviceFlowPollHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DeviceFlowPollRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request body", nil)
			return
		}

		if req.PollToken == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "poll_token is required", map[string]any{"field": "poll_token"})
			return
		}

		// TODO: Implement actual polling logic
		// For now, return a placeholder response
		resp := auth.DevicePollResponse{
			AccessToken: "placeholder_token",
			TokenType:   "Bearer",
			ExpiresIn:   86400,
			Team: auth.TeamInfo{
				ID:   "team-1",
				Slug: "personal-user",
				Name: "User's Team",
			},
			AvailableTools: auth.FilterToolsByDefault(true),
			NextStep:       "grant_tools",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

// AuthorizeToolsRequest is the request to authorize specific tools
type AuthorizeToolsRequest struct {
	Tools []string `json:"tools"`
}

func authorizeToolsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AuthorizeToolsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request body", nil)
			return
		}

		// Validate tool names
		if err := auth.ValidateToolGrants(req.Tools); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid tools", map[string]any{"details": err.Error()})
			return
		}

		// TODO: Store tool grants in database
		// TODO: Create final access token

		resp := map[string]interface{}{
			"access_token":   "final_token",
			"token_type":     "Bearer",
			"expires_in":     86400,
			"granted_tools":  req.Tools,
			"auth_status":    "authorized",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}
