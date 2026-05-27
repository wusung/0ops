package server

import (
	"context"
	"testing"
	"time"
)

func TestValidateTokenEmpty(t *testing.T) {
	_, err := ValidateToken("")
	if err == nil {
		t.Error("expected error for empty token")
	}
}

func TestValidateTokenWithContent(t *testing.T) {
	claims, err := ValidateToken("placeholder_token")
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	if claims == nil {
		t.Fatal("claims is nil")
	}

	if claims.UserID != "placeholder_user" {
		t.Errorf("UserID = %q, want %q", claims.UserID, "placeholder_user")
	}

	if claims.TeamID != "placeholder_team" {
		t.Errorf("TeamID = %q, want %q", claims.TeamID, "placeholder_team")
	}
}

func TestMCPAuthContextIsToolGranted(t *testing.T) {
	auth := &MCPAuthContext{
		UserID: "user-1",
		TeamID: "team-1",
		GrantedTools: map[string]bool{
			"list_teams": true,
			"list_apps":  true,
		},
	}

	if !auth.IsToolGranted("list_teams") {
		t.Error("expected IsToolGranted to return true for list_teams")
	}

	if auth.IsToolGranted("create_app") {
		t.Error("expected IsToolGranted to return false for create_app")
	}
}

func TestMCPAuthContextGetGrantedToolCount(t *testing.T) {
	auth := &MCPAuthContext{
		UserID: "user-1",
		TeamID: "team-1",
		GrantedTools: map[string]bool{
			"list_teams": true,
			"list_apps":  true,
		},
	}

	if count := auth.GetGrantedToolCount(); count != 2 {
		t.Errorf("GetGrantedToolCount() = %d, want 2", count)
	}
}

func TestMCPAuthContextNil(t *testing.T) {
	var auth *MCPAuthContext

	if auth.IsToolGranted("list_teams") {
		t.Error("expected IsToolGranted to return false for nil context")
	}

	if auth.GetGrantedToolCount() != 0 {
		t.Error("expected GetGrantedToolCount to return 0 for nil context")
	}
}

func TestTokenClaimsJSONRoundTrip(t *testing.T) {
	now := time.Now()
	claims := &TokenClaims{
		UserID:       "user-123",
		TeamID:       "team-456",
		GrantedTools: []string{"list_teams", "list_apps"},
		ExpiresAt:    now.Add(24 * time.Hour),
		IssuedAt:     now,
	}

	data, err := claims.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	var decoded TokenClaims
	if err := decoded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}

	if decoded.UserID != claims.UserID {
		t.Errorf("UserID mismatch: %q != %q", decoded.UserID, claims.UserID)
	}

	if decoded.TeamID != claims.TeamID {
		t.Errorf("TeamID mismatch: %q != %q", decoded.TeamID, claims.TeamID)
	}

	if len(decoded.GrantedTools) != len(claims.GrantedTools) {
		t.Errorf("GrantedTools length mismatch: %d != %d", len(decoded.GrantedTools), len(claims.GrantedTools))
	}
}

func TestGetAuthContextFromRequestNil(t *testing.T) {
	ctx := context.Background()
	auth, err := GetAuthContextFromRequest(ctx, nil)
	if err == nil {
		t.Error("expected error for nil request")
	}
	if auth != nil {
		t.Error("expected nil auth context")
	}
}
