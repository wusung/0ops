package sso

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
)

// Authorize handles GET /v1/auth/sso/{team_slug}/authorize — the OIDC login
// entry point. It is unauthenticated (login start carries no 0ops token): it
// generates a one-time state + PKCE code_verifier, saves the correlation, and
// 302-redirects the browser to the IdP authorization endpoint. The callback
// (same StateStore) consumes the state and exchanges the code with the stored
// verifier (spec § 8.1, § 12; design release/2026-06-30-oidc-login-and-e2e.md).
//
// State lives in the process-local StateStore, which is correct for a single
// replica (dev / e2e / single-process prod). Multi-replica HA requires a
// durable shared StateStore — a narrowed deferral, see the release doc § 6.
func (s *Service) Authorize(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "team_slug")
	team, err := s.Store.ResolveTeamBySlug(r.Context(), slug)
	if err != nil {
		apperror.Write(w, "team_not_found", apperror.ClassNotFound, "team not found", nil)
		return
	}
	cfg, err := s.Store.GetIdPConfigByTeam(r.Context(), team.ID)
	if err != nil {
		apperror.Write(w, "sso_not_configured", apperror.ClassNotFound, "team has no SSO configuration", nil)
		return
	}

	state, err := randToken()
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to generate state", nil)
		return
	}
	verifier, err := randToken()
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to generate verifier", nil)
		return
	}
	challenge := pkceChallenge(verifier)
	redirectURI := s.callbackRedirectURI(slug)

	disc, err := s.Verifier.Discover(r.Context(), cfg.Issuer)
	if err != nil {
		apperror.Write(w, "sso_idp_unreachable", apperror.ClassUnprocessable, "failed to reach IdP discovery", nil)
		return
	}
	authURL, err := BuildAuthorizeURL(disc, cfg, redirectURI, state, challenge)
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to build authorize URL", nil)
		return
	}

	// Save only after the URL is built — nothing to clean up on earlier failure.
	s.State.Save(state, StateData{TeamSlug: slug, RedirectURI: redirectURI, CodeVerifier: verifier})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// pkceChallenge derives the S256 PKCE code_challenge from a code_verifier
// (RFC 7636: base64url(sha256(verifier)), no padding).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
