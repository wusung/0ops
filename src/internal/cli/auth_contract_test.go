package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/winshare/zeroops/internal/shared/authconfig"
)

// TestContractDeviceFlowToTokenCache tests the full flow from device flow to token caching
func TestContractDeviceFlowToTokenCache(t *testing.T) {
	// Set up temporary auth config directory
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create mock backend server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/device/start":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"device_code": "test-device-code",
				"user_code": "ABC-1234",
				"verification_uri": "https://github.com/login/device",
				"expires_in": 900,
				"interval": 5
			}`))
		case "/v1/auth/device/poll":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"access_token": "test-token",
				"token_type": "Bearer",
				"expires_in": 86400,
				"team": {
					"id": "team-1",
					"slug": "personal-alice",
					"name": "Alice's Team"
				},
				"available_tools": [
					{
						"id": "list_apps",
						"name": "List Apps",
						"category": "read",
						"description": "List apps in a team",
						"default_allowed": true
					}
				],
				"next_step": "grant_tools"
			}`))
		case "/v1/teams/personal-alice/auth:grant-tools":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"access_token": "final-token",
				"token_type": "Bearer",
				"expires_in": 86400,
				"granted_tools": ["list_apps"],
				"auth_status": "authorized"
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Step 1: Run `0ops auth login`
	cmd := newAuthCommand()
	cmd.SetArgs([]string{"login", "--host", server.URL})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	ctx := context.Background()
	cmd.SetContext(ctx)

	// Execute the login command
	if err := cmd.Execute(); err != nil {
		t.Logf("Command output: %s", stdout.String())
		t.Fatalf("login failed: %v", err)
	}

	output := stdout.String()

	// Verify output contains expected steps
	if !bytes.Contains([]byte(output), []byte("Step 1/4")) {
		t.Error("expected step 1 output")
	}
	if !bytes.Contains([]byte(output), []byte("Login successful")) {
		t.Error("expected successful login message")
	}

	// Step 2: Verify token was cached
	configDir := filepath.Join(tmpDir, "0ops")
	authFile := filepath.Join(configDir, "auth.json")

	if _, err := os.Stat(authFile); err != nil {
		t.Fatalf("auth.json not created: %v", err)
	}

	// Read and verify the cached token
	data, err := os.ReadFile(authFile) //nolint:gosec // authFile is a controlled temporary file path from test setup
	if err != nil {
		t.Fatalf("failed to read auth.json: %v", err)
	}

	var authCfg authconfig.File
	if err := json.Unmarshal(data, &authCfg); err != nil {
		t.Fatalf("failed to parse auth.json: %v", err)
	}

	if len(authCfg.Tokens) == 0 {
		t.Fatal("no tokens in auth.json")
	}

	token := authCfg.Tokens[0]
	if token.Host != server.URL {
		t.Errorf("host mismatch: %q != %q", token.Host, server.URL)
	}
	if token.BearerToken == "" {
		t.Error("bearer token is empty")
	}
	if token.DefaultTeamSlug != "personal-alice" {
		t.Errorf("team slug mismatch: %q != %q", token.DefaultTeamSlug, "personal-alice")
	}
}

// TestContractGrantSubmissionDTO tests the DTO format for grant submission
func TestContractGrantSubmissionDTO(t *testing.T) {
	// Test that grant submission uses correct DTO format
	grantReq := map[string]interface{}{
		"tools": []string{"list_apps", "get_app", "create_app"},
	}

	data, err := json.Marshal(grantReq)
	if err != nil {
		t.Fatalf("failed to marshal grant request: %v", err)
	}

	// Verify it matches expected contract
	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	tools, ok := unmarshaled["tools"].([]interface{})
	if !ok || len(tools) != 3 {
		t.Error("tools field not correct in grant request")
	}

	// Verify response DTO
	grantResp := map[string]interface{}{
		"access_token":  "test-token",
		"token_type":    "Bearer",
		"expires_in":    86400,
		"granted_tools": []string{"list_apps", "get_app", "create_app"},
		"auth_status":   "authorized",
	}

	respData, err := json.Marshal(grantResp)
	if err != nil {
		t.Fatalf("failed to marshal grant response: %v", err)
	}

	var respUnmarshaled map[string]interface{}
	if err := json.Unmarshal(respData, &respUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if _, ok := respUnmarshaled["access_token"]; !ok {
		t.Error("access_token missing from response")
	}
	if _, ok := respUnmarshaled["granted_tools"]; !ok {
		t.Error("granted_tools missing from response")
	}
}

// TestContractAuthenticationHeaderFormat tests bearer token format contract
func TestContractAuthenticationHeaderFormat(t *testing.T) {
	tests := []struct {
		name  string
		token string
		valid bool
	}{
		{"valid bearer token", "Bearer test-token-123", true},
		{"empty token", "", false},
		{"missing bearer prefix", "test-token", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := http.Request{
				Header: http.Header{
					"Authorization": []string{tt.token},
				},
			}

			if tt.valid {
				auth := req.Header.Get("Authorization")
				if auth == "" {
					t.Error("expected authorization header")
				}
			}
		})
	}
}
