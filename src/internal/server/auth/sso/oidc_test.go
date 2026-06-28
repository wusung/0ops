package sso

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// mockIdP serves OIDC discovery + JWKS and signs id_tokens with a test RSA key.
type mockIdP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	idp := &mockIdP{key: key, kid: "test-kid-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 idp.issuer,
			"authorization_endpoint": idp.issuer + "/authorize",
			"token_endpoint":         idp.issuer + "/token",
			"jwks_uri":               idp.issuer + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{"kty": "RSA", "kid": idp.kid, "alg": "RS256", "use": "sig", "n": n, "e": e}},
		})
	})
	srv := httptest.NewServer(mux)
	idp.server = srv
	idp.issuer = srv.URL
	t.Cleanup(srv.Close)
	return idp
}

func (m *mockIdP) signIDToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = m.kid
	s, err := tok.SignedString(m.key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestVerifyIDTokenHappyPath(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, err := v.Discover(context.Background(), idp.issuer)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if disc.AuthorizationEndpoint == "" || disc.JWKSURI == "" {
		t.Fatalf("discovery missing endpoints: %+v", disc)
	}

	raw := idp.signIDToken(t, jwt.MapClaims{
		"iss":    idp.issuer,
		"aud":    "client-123",
		"sub":    "sub-abc",
		"email":  "alice@acme.example",
		"groups": []string{"eng", "admins"},
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
	})
	claims, err := v.VerifyIDToken(context.Background(), disc, "client-123", raw, "groups")
	if err != nil {
		t.Fatalf("VerifyIDToken: %v", err)
	}
	if claims.Subject != "sub-abc" || claims.Email != "alice@acme.example" {
		t.Fatalf("claims = %+v", claims)
	}
	if len(claims.Groups) != 2 || claims.Groups[0] != "eng" {
		t.Fatalf("groups = %v", claims.Groups)
	}
}

func TestVerifyIDTokenRejectsWrongAudience(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, _ := v.Discover(context.Background(), idp.issuer)
	raw := idp.signIDToken(t, jwt.MapClaims{
		"iss": idp.issuer, "aud": "other-client", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.VerifyIDToken(context.Background(), disc, "client-123", raw, ""); err == nil {
		t.Fatal("expected audience rejection")
	}
}

func TestVerifyIDTokenRejectsExpired(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, _ := v.Discover(context.Background(), idp.issuer)
	raw := idp.signIDToken(t, jwt.MapClaims{
		"iss": idp.issuer, "aud": "client-123", "sub": "x",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := v.VerifyIDToken(context.Background(), disc, "client-123", raw, ""); err == nil {
		t.Fatal("expected expired rejection")
	}
}

func TestVerifyLogoutTokenHappyPath(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, _ := v.Discover(context.Background(), idp.issuer)
	raw := idp.signIDToken(t, jwt.MapClaims{
		"iss": idp.issuer, "aud": "client-123", "sub": "sub-9", "iat": time.Now().Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	})
	sub, err := v.VerifyLogoutToken(context.Background(), disc, "client-123", raw)
	if err != nil || sub != "sub-9" {
		t.Fatalf("VerifyLogoutToken = %q, %v; want sub-9", sub, err)
	}
}

func TestVerifyLogoutTokenRejectsReplayedIDToken(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, _ := v.Discover(context.Background(), idp.issuer)
	// A plain id_token (no events claim) must not be accepted as a logout token.
	idToken := idp.signIDToken(t, jwt.MapClaims{
		"iss": idp.issuer, "aud": "client-123", "sub": "sub-9",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	if _, err := v.VerifyLogoutToken(context.Background(), disc, "client-123", idToken); err == nil {
		t.Fatal("id_token without backchannel-logout event must be rejected")
	}
}

func TestVerifyLogoutTokenRejectsNonce(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, _ := v.Discover(context.Background(), idp.issuer)
	raw := idp.signIDToken(t, jwt.MapClaims{
		"iss": idp.issuer, "aud": "client-123", "sub": "sub-9", "iat": time.Now().Unix(),
		"nonce":  "n",
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	})
	if _, err := v.VerifyLogoutToken(context.Background(), disc, "client-123", raw); err == nil {
		t.Fatal("logout token carrying nonce must be rejected")
	}
}

func TestVerifyIDTokenRejectsForeignKey(t *testing.T) {
	idp := newMockIdP(t)
	v := NewVerifier(idp.server.Client())
	disc, _ := v.Discover(context.Background(), idp.issuer)

	// Sign with a different key than the one published in JWKS.
	foreign, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": idp.issuer, "aud": "client-123", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = idp.kid
	raw, _ := tok.SignedString(foreign)
	if _, err := v.VerifyIDToken(context.Background(), disc, "client-123", raw, ""); err == nil {
		t.Fatal("expected signature rejection for foreign key")
	}
}
