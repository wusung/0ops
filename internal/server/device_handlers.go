package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

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

func authorizeToolsHandler(store toolGrantsStore) http.HandlerFunc {
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

		// Extract team_slug from URL params
		teamSlug := chi.URLParam(r, "team_slug")
		if teamSlug == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "team_slug is required", nil)
			return
		}

		// TODO: Extract user_id from temporary token
		// For now, use a placeholder (this will be implemented when device flow is complete)
		userID := "placeholder_user_id"
		teamID := "placeholder_team_id"

		// TODO: Resolve team_id from team_slug

		// Store tool grants
		for _, tool := range req.Tools {
			// Set all requested tools as allowed
			if err := store.UpsertToolGrant(r.Context(), teamID, userID, tool, true, nil); err != nil {
				apperror.Write(w, "database_error", apperror.ClassInternal, fmt.Sprintf("failed to grant tool %s", tool), nil)
				return
			}
		}

		// TODO: Create final access token with tool grants
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

// PatchToolGrantsRequest is the request body for PATCH /v1/me/auth/tool-grants
type PatchToolGrantsRequest struct {
	Grant  []string `json:"grant,omitempty"`
	Revoke []string `json:"revoke,omitempty"`
}

// PatchToolGrantsResponse is the response body for PATCH /v1/me/auth/tool-grants
type PatchToolGrantsResponse struct {
	GrantedTools []string `json:"granted_tools"`
	RevokedTools []string `json:"revoked_tools"`
}

func patchToolGrantsHandler(store toolGrantsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchToolGrantsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request body", nil)
			return
		}

		// Validate tool names if any are provided
		allTools := append(req.Grant, req.Revoke...)
		if err := auth.ValidateToolGrants(allTools); err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid tools", map[string]any{"details": err.Error()})
			return
		}

		// TODO: Extract user_id from Bearer token
		userID := "placeholder_user_id"
		teamID := "placeholder_team_id"

		// Grant tools
		for _, tool := range req.Grant {
			if err := store.UpsertToolGrant(r.Context(), teamID, userID, tool, true, nil); err != nil {
				apperror.Write(w, "database_error", apperror.ClassInternal, fmt.Sprintf("failed to grant tool %s", tool), nil)
				return
			}
		}

		// Revoke tools
		for _, tool := range req.Revoke {
			if err := store.RevokeToolGrant(r.Context(), teamID, userID, tool); err != nil {
				apperror.Write(w, "database_error", apperror.ClassInternal, fmt.Sprintf("failed to revoke tool %s", tool), nil)
				return
			}
		}

		resp := PatchToolGrantsResponse{
			GrantedTools: req.Grant,
			RevokedTools: req.Revoke,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}
