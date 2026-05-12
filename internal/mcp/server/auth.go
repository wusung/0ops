package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TokenClaims represents the JWT/bearer token claims structure
type TokenClaims struct {
	UserID       string    `json:"user_id"`
	TeamID       string    `json:"team_id"`
	GrantedTools []string  `json:"granted_tools"`
	ExpiresAt    time.Time `json:"exp"`
	IssuedAt     time.Time `json:"iat"`
}

// MCPAuthContext holds the authentication context for MCP requests
type MCPAuthContext struct {
	UserID       string
	TeamID       string
	GrantedTools map[string]bool
	Token        string
	ExpiresAt    time.Time
}

// ExtractBearerToken extracts the bearer token from the Authorization header
func ExtractBearerToken(request *mcp.CallToolRequest) (string, error) {
	if request == nil {
		return "", fmt.Errorf("request is nil")
	}

	// Check if Authorization header is present
	// Note: MCP request headers may be passed via request metadata or context
	// For now, this is a placeholder that expects token to be passed in request context
	return "", fmt.Errorf("bearer token extraction not yet implemented for MCP requests")
}

// ValidateToken validates a bearer token and extracts claims
// TODO: Implement actual token validation and JWT parsing
func ValidateToken(token string) (*TokenClaims, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("token is empty")
	}

	// Placeholder validation - should verify JWT signature
	// For now, just check that token exists
	claims := &TokenClaims{
		UserID:       "placeholder_user",
		TeamID:       "placeholder_team",
		GrantedTools: []string{},
		ExpiresAt:    time.Now().Add(24 * time.Hour),
		IssuedAt:     time.Now(),
	}

	return claims, nil
}

// GetAuthContextFromRequest extracts authentication context from MCP request
func GetAuthContextFromRequest(_ context.Context, request *mcp.CallToolRequest) (*MCPAuthContext, error) {
	// Extract token from request (implementation depends on MCP request structure)
	token, err := ExtractBearerToken(request)
	if err != nil {
		return nil, fmt.Errorf("failed to extract token: %w", err)
	}

	// Validate and parse token
	claims, err := ValidateToken(token)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	// Check token expiration
	if time.Now().After(claims.ExpiresAt) {
		return nil, fmt.Errorf("token expired")
	}

	// Build granted tools map for efficient lookup
	grantedTools := make(map[string]bool)
	for _, tool := range claims.GrantedTools {
		grantedTools[tool] = true
	}

	return &MCPAuthContext{
		UserID:       claims.UserID,
		TeamID:       claims.TeamID,
		GrantedTools: grantedTools,
		Token:        token,
		ExpiresAt:    claims.ExpiresAt,
	}, nil
}

// IsToolGranted checks if a user has permission to use a specific tool
func (auth *MCPAuthContext) IsToolGranted(toolName string) bool {
	if auth == nil {
		return false
	}
	return auth.GrantedTools[toolName]
}

// GetGrantedToolCount returns the number of tools granted to this user
func (auth *MCPAuthContext) GetGrantedToolCount() int {
	if auth == nil {
		return 0
	}
	return len(auth.GrantedTools)
}

// MarshalJSON for TokenClaims serialization
func (tc *TokenClaims) MarshalJSON() ([]byte, error) {
	type Alias TokenClaims
	return json.Marshal(&struct {
		ExpiresAt int64 `json:"exp"`
		IssuedAt  int64 `json:"iat"`
		*Alias
	}{
		ExpiresAt: tc.ExpiresAt.Unix(),
		IssuedAt:  tc.IssuedAt.Unix(),
		Alias:     (*Alias)(tc),
	})
}

// UnmarshalJSON for TokenClaims deserialization
func (tc *TokenClaims) UnmarshalJSON(data []byte) error {
	type Alias TokenClaims
	aux := &struct {
		ExpiresAt int64 `json:"exp"`
		IssuedAt  int64 `json:"iat"`
		*Alias
	}{
		Alias: (*Alias)(tc),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	tc.ExpiresAt = time.Unix(aux.ExpiresAt, 0)
	tc.IssuedAt = time.Unix(aux.IssuedAt, 0)
	return nil
}
