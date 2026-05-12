package cli

import (
	"bytes"
	"context"
	"testing"
)


func TestAuthLoginCommand(t *testing.T) {
	cmd := newAuthCommand()
	cmd.SetArgs([]string{"login"})

	// Capture output
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

	// Check that key phrases are in the output
	if !bytes.Contains([]byte(output), []byte("Device Flow")) {
		t.Error("expected 'Device Flow' in output")
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
	cmd := newAuthCommand()
	cmd.SetArgs([]string{"grant", "list_apps", "--host", "http://localhost:8080", "--token", "test-token"})

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
}

func TestAuthRevokeCommand(t *testing.T) {
	cmd := newAuthCommand()
	cmd.SetArgs([]string{"revoke", "create_app", "--host", "http://localhost:8080", "--token", "test-token"})

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
