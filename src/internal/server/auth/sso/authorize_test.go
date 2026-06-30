package sso

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/db"
)

// TestAuthorizeRedirectsToIdP pins the OIDC login entry: GET .../sso/{slug}/authorize
// generates state + PKCE, saves the correlation, and 302-redirects to the IdP
// authorization endpoint carrying response_type=code, the team's client_id, a
// callback redirect_uri, state, and an S256 code_challenge. The saved state must
// carry a code_verifier whose S256 hash equals the redirected code_challenge —
// proving the authorize→callback PKCE hop is internally consistent.
func TestAuthorizeRedirectsToIdP(t *testing.T) {
	idp := newMockIdP(t)
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: idp.issuer, ClientID: "cid", ClientSecretRef: "sso/team-acme/client_secret", JITDefaultRole: "member", PATPolicy: "allow", SessionMaxTTLS: 28800}

	verifier := NewVerifier(idp.server.Client())
	state := NewMemoryStateStore()
	svc, _ := newSvc(f, verifier, stubExchanger{}, state)
	srv, _ := newServer(t, f, svc)

	// Do NOT follow the redirect; assert the 302 itself.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/v1/auth/sso/acme/authorize")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", res.StatusCode)
	}

	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	idpURL, _ := url.Parse(idp.issuer)
	if loc.Host != idpURL.Host {
		t.Fatalf("redirect host = %q, want IdP host %q", loc.Host, idpURL.Host)
	}
	q := loc.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("client_id") != "cid" {
		t.Errorf("client_id = %q, want cid", q.Get("client_id"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/v1/auth/sso/acme/callback") {
		t.Errorf("redirect_uri = %q, want .../sso/acme/callback", q.Get("redirect_uri"))
	}
	gotState := q.Get("state")
	if gotState == "" {
		t.Fatal("state is empty")
	}

	// The saved state must exist and its code_verifier must hash (S256) to the
	// redirected code_challenge.
	saved, ok := state.Consume(gotState)
	if !ok {
		t.Fatal("state was not saved")
	}
	if saved.CodeVerifier == "" {
		t.Fatal("saved code_verifier is empty")
	}
	sum := sha256.Sum256([]byte(saved.CodeVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if q.Get("code_challenge") != wantChallenge {
		t.Errorf("code_challenge = %q, does not match S256(code_verifier)", q.Get("code_challenge"))
	}
	if saved.RedirectURI != q.Get("redirect_uri") {
		t.Errorf("saved redirect_uri = %q, redirect param = %q", saved.RedirectURI, q.Get("redirect_uri"))
	}
}

// TestAuthorizeUnconfiguredTeamIs404 pins fail-closed when the team has no IdP.
func TestAuthorizeUnconfiguredTeamIs404(t *testing.T) {
	f := baseFake() // f.cfg == nil → GetIdPConfigByTeam returns ErrNoRows
	svc, _ := newSvc(f, NewVerifier(nil), stubExchanger{}, nil)
	srv, _ := newServer(t, f, svc)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/v1/auth/sso/acme/authorize")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unconfigured authorize status = %d, want 404", res.StatusCode)
	}
}
