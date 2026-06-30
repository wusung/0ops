// Package main is a dev/e2e-only minimal OIDC identity provider. It serves
// discovery + JWKS, auto-approves the authorization request (302 back to the
// caller's redirect_uri with code + state), and issues an RS256 id_token at the
// token endpoint. It exists so tasks/e2e-sso.sh can drive the full OIDC login
// dance against the compose stack with no real IdP and no network egress.
//
// NEVER included in production images; the e2e compose overlay builds it.
//
// Contract mirrors the server's sso.Verifier expectations (RS256, kid in JWKS,
// iss == issuer, aud == client_id). See
// docs/features/sso-saml/release/2026-06-30-oidc-login-and-e2e.md § 3.
//
// Config (env):
//
//	MOCK_IDP_ADDR    listen address (default :9000)
//	MOCK_IDP_ISSUER  externally-reachable issuer URL the server uses for
//	                 discovery AND matches the id_token iss (default
//	                 http://mock-idp:9000). MUST equal the idp_config.issuer.
//	MOCK_IDP_SUBJECT id_token sub (default mock-sub-1)
//	MOCK_IDP_EMAIL   id_token email, email_verified=true (default alice@acme.example)
//	MOCK_IDP_GROUPS  optional comma-separated groups claim
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const kid = "mock-idp-kid-1"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := env("MOCK_IDP_ADDR", ":9000")

	// Shell-free healthcheck mode for the distroless image's HEALTHCHECK:
	// `mock-idp healthcheck` GETs /healthz on the local port and exits 0/1.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		port := addr
		if i := strings.LastIndex(addr, ":"); i >= 0 {
			port = addr[i:]
		}
		res, err := http.Get("http://127.0.0.1" + port + "/healthz")
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	issuer := strings.TrimRight(env("MOCK_IDP_ISSUER", "http://mock-idp:9000"), "/")
	subject := env("MOCK_IDP_SUBJECT", "mock-sub-1")
	email := env("MOCK_IDP_EMAIL", "alice@acme.example")
	var groups []string
	if g := env("MOCK_IDP_GROUPS", ""); g != "" {
		groups = strings.Split(g, ",")
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("mock-idp: gen key: %v", err)
	}

	mux := http.NewServeMux()

	// Discovery: all endpoints point back at the issuer so the server (which
	// reaches the IdP via compose DNS) resolves them to this process.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/authorize",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/jwks",
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		writeJSON(w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig", "n": n, "e": e,
			}},
		})
	})

	// Authorize: auto-approve. Echo state back and mint an opaque code; the
	// token endpoint is stateless (single configured user) so the code is
	// cosmetic but required by the OIDC flow.
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirectURI := q.Get("redirect_uri")
		state := q.Get("state")
		if redirectURI == "" {
			http.Error(w, "missing redirect_uri", http.StatusBadRequest)
			return
		}
		dest, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)
			return
		}
		dq := dest.Query()
		dq.Set("code", "mock-code-"+randHex())
		if state != "" {
			dq.Set("state", state)
		}
		dest.RawQuery = dq.Encode()
		http.Redirect(w, r, dest.String(), http.StatusFound)
	})

	// Token: validate grant_type, mirror the request's client_id into aud so
	// the server's audience check passes for whatever client it configured.
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.PostFormValue("grant_type") != "authorization_code" {
			http.Error(w, "unsupported_grant_type", http.StatusBadRequest)
			return
		}
		if r.PostFormValue("code") == "" {
			http.Error(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		aud := r.PostFormValue("client_id")
		now := time.Now()
		claims := jwt.MapClaims{
			"iss":            issuer,
			"aud":            aud,
			"sub":            subject,
			"email":          email,
			"email_verified": true,
			"iat":            now.Unix(),
			"exp":            now.Add(time.Hour).Unix(),
		}
		if len(groups) > 0 {
			claims["groups"] = groups
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = kid
		signed, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, "sign error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": "mock-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     signed,
		})
	})

	// Liveness for the compose healthcheck.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("mock-idp: listening on %s, issuer=%s, email=%s", addr, issuer, email)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("mock-idp: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
