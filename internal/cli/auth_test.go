package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)


func TestAuthLoginCommand(t *testing.T) {
	// Create a mock backend server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/device/start":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"device_code": "test-device-code",
				"user_code": "ABC-1234",
				"verification_uri": "https://github.com/login/device",
				"expires_in": 900,
				"interval": 5
			}`))
		case "/v1/auth/device/poll":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
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
			w.Write([]byte(`{
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

	cmd := newAuthCommand()
	cmd.SetArgs([]string{"login", "--host", server.URL})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	ctx := context.Background()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("expected output from login command")
	}

	// Check for key phrases
	if !bytes.Contains([]byte(output), []byte("Step 1/4")) {
		t.Error("expected step output in login flow")
	}
}

func TestAuthStatusCommand(t *testing.T) {
	cmd := newAuthCommand()
	cmd.SetArgs([]string{"status", "--host", "http://localhost:8080", "--token", "test-token"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	ctx := context.Background()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("expected output from status command")
	}

	if !bytes.Contains([]byte(output), []byte("Authentication Status")) {
		t.Error("expected 'Authentication Status' in output")
	}
}

func TestAuthGrantCommand(t *testing.T) {
	// Create a mock backend server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/v1/me/auth/tool-grants" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"granted_tools": ["list_apps"],
				"revoked_tools": []
			}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cmd := newAuthCommand()
	cmd.SetArgs([]string{"grant", "list_apps", "--host", server.URL, "--token", "test-token"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	ctx := context.Background()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	if !bytes.Contains([]byte(output), []byte("list_apps")) {
		t.Error("expected 'list_apps' in output")
	}
	if !bytes.Contains([]byte(output), []byte("Granted")) {
		t.Error("expected 'Granted' in output")
	}
}

func TestAuthRevokeCommand(t *testing.T) {
	// Create a mock backend server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/v1/me/auth/tool-grants" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"granted_tools": ["list_apps"],
				"revoked_tools": ["create_app"]
			}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cmd := newAuthCommand()
	cmd.SetArgs([]string{"revoke", "create_app", "--host", server.URL, "--token", "test-token"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	ctx := context.Background()
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	if !bytes.Contains([]byte(output), []byte("create_app")) {
		t.Error("expected 'create_app' in output")
	}
	if !bytes.Contains([]byte(output), []byte("Revoked")) {
		t.Error("expected 'Revoked' in output")
	}
}

func TestAuthCommandRootHelp(t *testing.T) {
	cmd := newAuthCommand()

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	cmd.SetArgs([]string{"-h"})
	_ = cmd.Execute()

	output := stdout.String()
	if !bytes.Contains([]byte(output), []byte("login")) {
		t.Error("expected 'login' subcommand in help")
	}
	if !bytes.Contains([]byte(output), []byte("status")) {
		t.Error("expected 'status' subcommand in help")
	}
	if !bytes.Contains([]byte(output), []byte("grant")) {
		t.Error("expected 'grant' subcommand in help")
	}
	if !bytes.Contains([]byte(output), []byte("revoke")) {
		t.Error("expected 'revoke' subcommand in help")
	}
}

func TestAuthGrantEmptyTool(t *testing.T) {
	cmd := newAuthCommand()
	cmd.SetArgs([]string{"grant", "", "--host", "http://localhost:8080", "--token", "test-token"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	ctx := context.Background()
	cmd.SetContext(ctx)

	// Should fail because empty tool name
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for empty tool name")
	}
}
