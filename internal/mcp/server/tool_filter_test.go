package server

import (
	"testing"
)

func TestNewToolRegistry(t *testing.T) {
	registry := NewToolRegistry()
	if registry == nil {
		t.Fatal("expected non-nil registry")
	}

	allTools := registry.GetAllTools()
	if len(allTools) != 8 {
		t.Errorf("expected 8 tools, got %d", len(allTools))
	}
}

func TestGetTool(t *testing.T) {
	registry := NewToolRegistry()

	tool, err := registry.GetTool("list_teams")
	if err != nil {
		t.Fatalf("GetTool() error = %v", err)
	}

	if tool.Name != "list_teams" {
		t.Errorf("tool name = %q, want %q", tool.Name, "list_teams")
	}

	if !tool.DefaultAllow {
		t.Error("expected list_teams to be default-allow")
	}
}

func TestGetToolNotFound(t *testing.T) {
	registry := NewToolRegistry()

	_, err := registry.GetTool("nonexistent_tool")
	if err == nil {
		t.Error("expected error for nonexistent tool")
	}
}

func TestGetToolsByCategory(t *testing.T) {
	registry := NewToolRegistry()

	readTools := registry.GetToolsByCategory("read")
	if len(readTools) < 3 {
		t.Errorf("expected at least 3 read tools, got %d", len(readTools))
	}

	writeTools := registry.GetToolsByCategory("write")
	if len(writeTools) != 2 {
		t.Errorf("expected 2 write tools, got %d", len(writeTools))
	}

	deleteTools := registry.GetToolsByCategory("delete")
	if len(deleteTools) != 2 {
		t.Errorf("expected 2 delete tools, got %d", len(deleteTools))
	}
}

func TestGetToolsForUserNoGrants(t *testing.T) {
	registry := NewToolRegistry()

	tools := registry.GetToolsForUser(nil)
	if len(tools) == 0 {
		t.Error("expected at least one default-allow tool")
	}

	// All returned tools should be default-allow
	for _, tool := range tools {
		if !tool.DefaultAllow {
			t.Errorf("non-default-allow tool returned: %s", tool.Name)
		}
	}
}

func TestGetToolsForUserWithGrants(t *testing.T) {
	registry := NewToolRegistry()

	// Grant some write tools
	grantedTools := []string{"invite_member_preview", "invite_member"}
	tools := registry.GetToolsForUser(grantedTools)

	// Should include default-allow + granted
	if len(tools) < 5 {
		t.Errorf("expected at least 5 tools (default-allow + granted), got %d", len(tools))
	}

	// Check that granted tools are included
	hasInvitePreview := false
	for _, tool := range tools {
		if tool.Name == "invite_member_preview" {
			hasInvitePreview = true
		}
	}
	if !hasInvitePreview {
		t.Error("expected granted tool invite_member_preview to be included")
	}
}

func TestIsToolGrantedDefaultAllow(t *testing.T) {
	registry := NewToolRegistry()

	// list_teams is default-allow
	granted, err := registry.IsToolGranted("list_teams", nil)
	if err != nil {
		t.Fatalf("IsToolGranted() error = %v", err)
	}
	if !granted {
		t.Error("expected list_teams to be granted (default-allow)")
	}
}

func TestIsToolGrantedExplicitGrant(t *testing.T) {
	registry := NewToolRegistry()

	// invite_member is not default-allow, but explicitly granted
	grantedTools := []string{"invite_member"}
	granted, err := registry.IsToolGranted("invite_member", grantedTools)
	if err != nil {
		t.Fatalf("IsToolGranted() error = %v", err)
	}
	if !granted {
		t.Error("expected invite_member to be granted explicitly")
	}
}

func TestIsToolGrantedNotGranted(t *testing.T) {
	registry := NewToolRegistry()

	// remove_member is not default-allow and not granted
	grantedTools := []string{"invite_member"}
	granted, err := registry.IsToolGranted("remove_member", grantedTools)
	if err != nil {
		t.Fatalf("IsToolGranted() error = %v", err)
	}
	if granted {
		t.Error("expected remove_member to not be granted")
	}
}

func TestIsToolGrantedInvalid(t *testing.T) {
	registry := NewToolRegistry()

	_, err := registry.IsToolGranted("nonexistent_tool", nil)
	if err == nil {
		t.Error("expected error for invalid tool")
	}
}

func TestValidateToolNames(t *testing.T) {
	registry := NewToolRegistry()

	validNames := []string{"list_teams", "list_apps"}
	if err := registry.ValidateToolNames(validNames); err != nil {
		t.Fatalf("ValidateToolNames() error = %v", err)
	}
}

func TestValidateToolNamesInvalid(t *testing.T) {
	registry := NewToolRegistry()

	invalidNames := []string{"list_teams", "nonexistent_tool"}
	if err := registry.ValidateToolNames(invalidNames); err == nil {
		t.Error("expected error for invalid tool name")
	}
}

func TestFilterUnauthorizedTools(t *testing.T) {
	registry := NewToolRegistry()

	toolNames := []string{"list_teams", "invite_member", "remove_member"}
	grantedTools := []string{"invite_member"}

	unauthorized := registry.FilterUnauthorizedTools(toolNames, grantedTools)
	if len(unauthorized) != 1 {
		t.Errorf("expected 1 unauthorized tool, got %d: %v", len(unauthorized), unauthorized)
	}
	if unauthorized[0] != "remove_member" {
		t.Errorf("expected remove_member to be unauthorized, got %v", unauthorized)
	}
}

func TestGetUnauthorizedToolError(t *testing.T) {
	registry := NewToolRegistry()

	toolNames := []string{"list_teams", "invite_member", "remove_member"}
	grantedTools := []string{"invite_member"}

	err := registry.GetUnauthorizedToolError(toolNames, grantedTools)
	if err == nil {
		t.Error("expected error for unauthorized tools")
	}

	if err.Error() != "tool not authorized: remove_member" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestGetUnauthorizedToolErrorMultiple(t *testing.T) {
	registry := NewToolRegistry()

	toolNames := []string{"list_teams", "remove_member", "remove_member_preview"}
	grantedTools := []string{}

	err := registry.GetUnauthorizedToolError(toolNames, grantedTools)
	if err == nil {
		t.Error("expected error for unauthorized tools")
	}

	// Should include both remove_member and remove_member_preview
	errMsg := err.Error()
	if errMsg != "tools not authorized: remove_member, remove_member_preview" && errMsg != "tools not authorized: remove_member_preview, remove_member" {
		t.Errorf("unexpected error message: %s", errMsg)
	}
}

func TestGetUnauthorizedToolErrorNone(t *testing.T) {
	registry := NewToolRegistry()

	toolNames := []string{"list_teams", "list_apps"}
	grantedTools := []string{}

	err := registry.GetUnauthorizedToolError(toolNames, grantedTools)
	if err != nil {
		t.Errorf("expected no error for all authorized tools, got: %v", err)
	}
}
