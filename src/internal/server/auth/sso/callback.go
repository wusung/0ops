package sso

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// Callback handles GET /v1/auth/sso/{team_slug}/callback. It is unauthenticated
// (the IdP redirect carries no 0ops token) but validates state + PKCE and the
// id_token signature before provisioning (spec § 8.1, § 12).
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
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

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "code and state are required", nil)
		return
	}
	stData, ok := s.State.Consume(state)
	if !ok {
		apperror.Write(w, "invalid_state", apperror.ClassBadRequest, "unknown or expired state", nil)
		return
	}

	secret, err := s.Secrets.Get(cfg.ClientSecretRef)
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to resolve client secret", nil)
		return
	}
	disc, err := s.Verifier.Discover(r.Context(), cfg.Issuer)
	if err != nil {
		apperror.Write(w, "sso_idp_unreachable", apperror.ClassUnprocessable, "failed to reach IdP discovery", nil)
		return
	}
	redirectURI := stData.RedirectURI
	if redirectURI == "" {
		redirectURI = s.callbackRedirectURI(slug)
	}
	rawIDToken, idpExpiry, err := s.Exchanger.Exchange(r.Context(), disc, cfg.ClientID, secret, code, redirectURI, stData.CodeVerifier)
	if err != nil {
		apperror.Write(w, "sso_code_exchange_failed", apperror.ClassUnprocessable, "authorization code exchange failed", nil)
		return
	}
	groupClaim := ""
	if cfg.GroupClaim != nil {
		groupClaim = *cfg.GroupClaim
	}
	claims, err := s.Verifier.VerifyIDToken(r.Context(), disc, cfg.ClientID, rawIDToken, groupClaim)
	if err != nil {
		s.auditLoginFailure(r, team.ID, "id_token_verify")
		apperror.Write(w, "sso_invalid_id_token", apperror.ClassUnprocessable, "id_token verification failed", nil)
		return
	}

	// Email must be present AND verified by the IdP before it can bind identity
	// (OIDC: email is not a trustworthy identifier unless email_verified is true).
	// This blocks linking a JIT login to an existing account via an unverified
	// address (spec § 6.1; review C1).
	if claims.Email == "" || !claims.EmailVerified {
		s.auditLoginFailure(r, team.ID, "email_unverified")
		apperror.Write(w, "sso_email_unverified", apperror.ClassForbidden, "id_token email is missing or not verified by the IdP", nil)
		return
	}

	// Email domain must be a verified domain for this IdP (spec § 6.1).
	dom := emailDomain(claims.Email)
	if dom == "" {
		s.auditLoginFailure(r, team.ID, "missing_email")
		apperror.Write(w, "sso_domain_mismatch", apperror.ClassForbidden, "id_token has no usable email domain", nil)
		return
	}
	okDomain, err := s.Store.IsDomainVerifiedForConfig(r.Context(), cfg.ID, dom)
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to check domain", nil)
		return
	}
	if !okDomain {
		s.auditLoginFailure(r, team.ID, "domain_mismatch")
		apperror.Write(w, "sso_domain_mismatch", apperror.ClassForbidden, "email domain is not a verified domain for this team", nil)
		return
	}

	role := ResolveJITRole(cfg, claims.Groups)
	jit, err := s.Store.JITProvision(r.Context(), db.JITParams{
		IdPConfigID: cfg.ID, TeamID: team.ID, Subject: claims.Subject, Email: claims.Email, Role: role,
	})
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to provision user", nil)
		return
	}

	now := s.clock()
	expiresAt := SSOTokenExpiry(now, idpExpiry, cfg.SessionMaxTTLS)
	bearer, err := s.Store.IssueSSOToken(r.Context(), jit.UserID, team.ID, cfg.ID, defaultSSOScopes(), expiresAt)
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to issue token", nil)
		return
	}

	if jit.ProvisionedMembership {
		uid := jit.UserID
		s.audit(r.Context(), audit.Entry{
			TeamID: team.ID, Source: audit.SourceSystem,
			SubjectType: "membership", SubjectID: &uid, Action: ActionSSOProvisionUser,
			Result: map[string]any{"role": jit.Role}, Outcome: audit.OutcomeSuccess,
		})
	}
	uid := jit.UserID
	s.audit(r.Context(), audit.Entry{
		TeamID: team.ID, ActorUserID: &uid, Source: audit.SourceUser,
		SubjectType: "team", SubjectID: &team.ID, Action: ActionSSOLogin,
		Args: map[string]any{"email": claims.Email}, Outcome: audit.OutcomeSuccess,
	})

	writeJSON(w, http.StatusOK, dto.SSOCallbackResponse{
		BearerToken: bearer,
		TeamSlug:    team.Slug,
		Email:       claims.Email,
		IssuedAt:    now,
		ExpiresAt:   expiresAt,
	})
}

func (s *Service) auditLoginFailure(r *http.Request, teamID, reason string) {
	s.audit(r.Context(), audit.Entry{
		TeamID: teamID, Source: audit.SourceUser,
		SubjectType: "team", SubjectID: &teamID, Action: ActionSSOLogin,
		Args: map[string]any{"reason": reason}, Outcome: audit.OutcomeFailure,
	})
}

// BackchannelLogout handles POST /v1/auth/sso/{team_slug}/backchannel-logout.
// The IdP-signed logout token identifies the subject to revoke; the row is
// audited with source=system + nil actor (hard rule #8).
func (s *Service) BackchannelLogout(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid form body", nil)
		return
	}
	logoutToken := strings.TrimSpace(r.PostFormValue("logout_token"))
	if logoutToken == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "logout_token is required", nil)
		return
	}
	disc, err := s.Verifier.Discover(r.Context(), cfg.Issuer)
	if err != nil {
		apperror.Write(w, "sso_idp_unreachable", apperror.ClassUnprocessable, "failed to reach IdP discovery", nil)
		return
	}
	// Logout-token-specific verification (events claim + no nonce) so a replayed
	// id_token cannot force a deprovision (spec § 7.1; review I1).
	subject, err := s.Verifier.VerifyLogoutToken(r.Context(), disc, cfg.ClientID, logoutToken)
	if err != nil {
		apperror.Write(w, "sso_invalid_logout_token", apperror.ClassUnprocessable, "logout token verification failed", nil)
		return
	}
	res, userID, err := s.Store.DeprovisionSSOUserBySubject(r.Context(), team.ID, cfg.ID, subject)
	if err != nil {
		// Unknown subject is a no-op success (idempotent back-channel).
		w.WriteHeader(http.StatusOK)
		return
	}
	uid := userID
	s.audit(r.Context(), audit.Entry{
		TeamID: team.ID, Source: audit.SourceSystem,
		SubjectType: "membership", SubjectID: &uid, Action: ActionSSOLogout,
		Result: map[string]any{"tokens_revoked": res.TokensRevoked}, Outcome: audit.OutcomeSuccess,
	})
	w.WriteHeader(http.StatusOK)
}
