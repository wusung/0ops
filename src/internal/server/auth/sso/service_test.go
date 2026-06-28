package sso

import (
	"net/url"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
)

func strp(s string) *string { return &s }

func TestResolveJITRoleCapsAtAdmin(t *testing.T) {
	cfg := db.IdPConfig{
		JITDefaultRole: "member",
		GroupClaim:     strp("groups"),
		GroupRoleMap:   map[string]string{"eng": "viewer", "leads": "owner", "ops": "admin"},
	}
	// "leads" maps to owner but JIT never grants owner — capped to admin (#3).
	if got := ResolveJITRole(cfg, []string{"eng", "leads"}); got != "admin" {
		t.Fatalf("owner mapping = %q, want admin (capped)", got)
	}
	// Highest of multiple matches wins (admin > viewer).
	if got := ResolveJITRole(cfg, []string{"eng", "ops"}); got != "admin" {
		t.Fatalf("multi-match = %q, want admin", got)
	}
	// No matching group falls back to default role.
	if got := ResolveJITRole(cfg, []string{"unknown"}); got != "member" {
		t.Fatalf("no-match = %q, want member", got)
	}
}

func TestResolveJITRoleDefaultWithoutGroups(t *testing.T) {
	cfg := db.IdPConfig{JITDefaultRole: "viewer"}
	if got := ResolveJITRole(cfg, nil); got != "viewer" {
		t.Fatalf("role = %q, want viewer", got)
	}
}

func TestPATDisabled(t *testing.T) {
	if !PATDisabled(db.IdPConfig{Enforce: true, PATPolicy: "disallow"}) {
		t.Fatal("enforce+disallow should disable PAT")
	}
	if PATDisabled(db.IdPConfig{Enforce: true, PATPolicy: "allow"}) {
		t.Fatal("allow policy must not disable PAT")
	}
	if PATDisabled(db.IdPConfig{Enforce: false, PATPolicy: "disallow"}) {
		t.Fatal("non-enforced team must not disable PAT")
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	disc := Discovery{AuthorizationEndpoint: "https://idp.example.com/authorize"}
	cfg := db.IdPConfig{ClientID: "cid", Scopes: []string{"openid", "email"}}
	raw, err := BuildAuthorizeURL(disc, cfg, "https://api/cb", "state-1", "chal-1")
	if err != nil {
		t.Fatalf("BuildAuthorizeURL: %v", err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "cid" {
		t.Fatalf("query = %v", q)
	}
	if q.Get("code_challenge") != "chal-1" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("missing PKCE params: %v", q)
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
}
