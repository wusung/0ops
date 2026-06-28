package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "invalid request body", nil)
		return false
	}
	return true
}

func randToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func secretRef(teamID string) string { return "sso/" + teamID + "/client_secret" }

func statusFromConfig(cfg db.IdPConfig, domains []db.IdPDomain) dto.SSOStatus {
	out := dto.SSOStatus{
		Protocol:       cfg.Protocol,
		Issuer:         cfg.Issuer,
		Enforce:        cfg.Enforce,
		JITDefaultRole: cfg.JITDefaultRole,
		PATPolicy:      cfg.PATPolicy,
		Domains:        make([]dto.SSODomainStatus, 0, len(domains)),
	}
	for _, d := range domains {
		out.Domains = append(out.Domains, dto.SSODomainStatus{Domain: d.Domain, Verified: d.Verified})
	}
	return out
}

func (s *Service) audit(ctx context.Context, e audit.Entry) {
	if s.Audit == nil {
		return
	}
	// Hard rule #8 requires SSO actions to be audited. The repo's write paths
	// warn-and-continue on audit-write failure (apps.go deploy callback); match
	// that so a failed write is at least surfaced, never silently dropped.
	if err := s.Audit.Log(ctx, e); err != nil {
		slog.Warn("sso audit write failed", "action", e.Action, "outcome", e.Outcome, "err", err)
	}
}

func strptr(s string) *string { return &s }

// CreateConfig handles POST /v1/teams/{slug}/sso (owner + sso:manage).
func (s *Service) CreateConfig(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	var req dto.SSOConfigRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Issuer) == "" || strings.TrimSpace(req.ClientID) == "" || strings.TrimSpace(req.ClientSecret) == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "issuer, client_id, client_secret are required", nil)
		return
	}
	ref := secretRef(teamID)
	if err := s.Secrets.Put(ref, req.ClientSecret); err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to store client secret", nil)
		return
	}
	params := db.CreateIdPConfigParams{
		TeamID:          teamID,
		Issuer:          strings.TrimRight(req.Issuer, "/"),
		ClientID:        req.ClientID,
		ClientSecretRef: ref,
		JITDefaultRole:  req.JITDefaultRole,
		PATPolicy:       req.PATPolicy,
		Scopes:          req.Scopes,
		GroupRoleMap:    req.GroupRoleMap,
		CreatedBy:       &actor,
	}
	if req.DisplayName != "" {
		params.DisplayName = strptr(req.DisplayName)
	}
	if req.DiscoveryURL != "" {
		params.DiscoveryURL = strptr(req.DiscoveryURL)
	}
	if req.GroupClaim != "" {
		params.GroupClaim = strptr(req.GroupClaim)
	}
	cfg, err := s.Store.CreateIdPConfig(r.Context(), params)
	if err != nil {
		if errors.Is(err, db.ErrIdPConfigExists) {
			apperror.Write(w, "sso_config_exists", apperror.ClassConflict, "team already has an IdP config", nil)
			return
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to create idp config", nil)
		return
	}
	// Secret is intentionally absent from audit args (hard rule #7).
	s.audit(r.Context(), audit.Entry{
		TeamID: teamID, ActorUserID: &actor, Source: audit.SourceUser,
		SubjectType: "idp_config", SubjectID: &cfg.ID, Action: ActionSSOConfigCreate,
		Args:    map[string]any{"issuer": cfg.Issuer, "client_id": cfg.ClientID},
		Outcome: audit.OutcomeSuccess,
	})
	writeJSON(w, http.StatusCreated, statusFromConfig(cfg, nil))
}

// GetStatus handles GET /v1/teams/{slug}/sso (admin + sso:manage).
func (s *Service) GetStatus(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	cfg, err := s.Store.GetIdPConfigByTeam(r.Context(), teamID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apperror.Write(w, "sso_not_configured", apperror.ClassNotFound, "team has no SSO configuration", nil)
			return
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to load idp config", nil)
		return
	}
	domains, err := s.Store.ListIdPDomains(r.Context(), cfg.ID)
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to load domains", nil)
		return
	}
	writeJSON(w, http.StatusOK, statusFromConfig(cfg, domains))
}

// UpdateConfig handles PATCH /v1/teams/{slug}/sso (owner + sso:manage).
func (s *Service) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	var req dto.SSOConfigPatchRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.Store.GetIdPConfigByTeam(r.Context(), teamID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apperror.Write(w, "sso_not_configured", apperror.ClassNotFound, "team has no SSO configuration", nil)
			return
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to load idp config", nil)
		return
	}
	// enforce requires at least one verified domain (spec § 5.2, hard rule #9).
	if req.Enforce != nil && *req.Enforce {
		has, herr := s.Store.HasVerifiedDomain(r.Context(), cfg.ID)
		if herr != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to check domains", nil)
			return
		}
		if !has {
			apperror.Write(w, "sso_no_verified_domain", apperror.ClassBadRequest, "enforce requires at least one verified domain", nil)
			return
		}
	}
	updated, err := s.Store.UpdateIdPConfig(r.Context(), teamID, db.IdPConfigPatch{
		Enforce: req.Enforce, PATPolicy: req.PATPolicy, JITDefaultRole: req.JITDefaultRole, DisplayName: req.DisplayName,
	})
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to update idp config", nil)
		return
	}
	s.audit(r.Context(), audit.Entry{
		TeamID: teamID, ActorUserID: &actor, Source: audit.SourceUser,
		SubjectType: "idp_config", SubjectID: &updated.ID, Action: ActionSSOConfigUpdate,
		Args:    map[string]any{"enforce": updated.Enforce, "pat_policy": updated.PATPolicy},
		Outcome: audit.OutcomeSuccess,
	})
	writeJSON(w, http.StatusOK, statusFromConfig(updated, nil))
}

// DeleteConfig handles DELETE /v1/teams/{slug}/sso (owner + sso:manage).
func (s *Service) DeleteConfig(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	cfg, err := s.Store.GetIdPConfigByTeam(r.Context(), teamID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apperror.Write(w, "sso_not_configured", apperror.ClassNotFound, "team has no SSO configuration", nil)
			return
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to load idp config", nil)
		return
	}
	if cfg.Enforce {
		apperror.Write(w, "sso_enforce_on", apperror.ClassConflict, "disable enforce before deleting the IdP config", nil)
		return
	}
	if err := s.Store.DeleteIdPConfig(r.Context(), teamID); err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to delete idp config", nil)
		return
	}
	s.audit(r.Context(), audit.Entry{
		TeamID: teamID, ActorUserID: &actor, Source: audit.SourceUser,
		SubjectType: "idp_config", SubjectID: &cfg.ID, Action: ActionSSOConfigDelete,
		Outcome: audit.OutcomeSuccess,
	})
	w.WriteHeader(http.StatusNoContent)
}

// AddDomain handles POST /v1/teams/{slug}/sso/domains (owner + sso:manage).
func (s *Service) AddDomain(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	var req dto.SSOAddDomainRequest
	if !decode(w, r, &req) {
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "domain is required", nil)
		return
	}
	cfg, err := s.Store.GetIdPConfigByTeam(r.Context(), teamID)
	if err != nil {
		apperror.Write(w, "sso_not_configured", apperror.ClassNotFound, "team has no SSO configuration", nil)
		return
	}
	token, err := randToken()
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to generate verification token", nil)
		return
	}
	dom, err := s.Store.AddIdPDomain(r.Context(), cfg.ID, teamID, domain, token)
	if err != nil {
		if errors.Is(err, db.ErrDomainTaken) {
			apperror.Write(w, "domain_taken", apperror.ClassConflict, "domain already bound to a team", nil)
			return
		}
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to add domain", nil)
		return
	}
	writeJSON(w, http.StatusCreated, dto.SSOAddDomainResponse{
		DomainID:       dom.ID,
		Domain:         dom.Domain,
		DNSRecordName:  "_0ops-sso." + dom.Domain,
		DNSRecordValue: "0ops-verify=" + token,
	})
}

// VerifyDomain handles POST /v1/teams/{slug}/sso/domains/{id}/verify.
func (s *Service) VerifyDomain(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	domainID := chi.URLParam(r, "domain_id")
	dom, err := s.Store.GetIdPDomain(r.Context(), domainID)
	if err != nil || dom.TeamID != teamID {
		apperror.Write(w, "domain_not_found", apperror.ClassNotFound, "domain not found", nil)
		return
	}
	records, err := s.lookupTXT(r.Context(), "_0ops-sso."+dom.Domain)
	if err == nil && containsToken(records, dom.VerificationToken) {
		verified, verr := s.Store.MarkIdPDomainVerified(r.Context(), dom.ID)
		if verr != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to mark domain verified", nil)
			return
		}
		s.audit(r.Context(), audit.Entry{
			TeamID: teamID, ActorUserID: &actor, Source: audit.SourceUser,
			SubjectType: "idp_config", SubjectID: &dom.IdPConfigID, Action: ActionSSODomainVerify,
			Args: map[string]any{"domain": dom.Domain}, Outcome: audit.OutcomeSuccess,
		})
		writeJSON(w, http.StatusOK, dto.SSODomainStatus{Domain: verified.Domain, Verified: true})
		return
	}
	s.audit(r.Context(), audit.Entry{
		TeamID: teamID, ActorUserID: &actor, Source: audit.SourceUser,
		SubjectType: "idp_config", SubjectID: &dom.IdPConfigID, Action: ActionSSODomainVerify,
		Args: map[string]any{"domain": dom.Domain}, Outcome: audit.OutcomeFailure,
	})
	apperror.Write(w, "sso_domain_not_verified", apperror.ClassBadRequest, "DNS TXT record not found or mismatched", map[string]any{
		"dns_record_name":  "_0ops-sso." + dom.Domain,
		"dns_record_value": "0ops-verify=" + dom.VerificationToken,
	})
}

// Deprovision handles POST /v1/teams/{slug}/sso/deprovision (owner + sso:manage).
func (s *Service) Deprovision(w http.ResponseWriter, r *http.Request) {
	teamID := auth.TeamID(r.Context())
	actor := auth.ActorUserID(r.Context())
	var req dto.SSODeprovisionRequest
	if !decode(w, r, &req) {
		return
	}
	user := strings.TrimSpace(req.User)
	if user == "" {
		apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "user is required", nil)
		return
	}
	cfg, err := s.Store.GetIdPConfigByTeam(r.Context(), teamID)
	if err != nil {
		apperror.Write(w, "sso_not_configured", apperror.ClassNotFound, "team has no SSO configuration", nil)
		return
	}
	userID := user
	if strings.Contains(user, "@") {
		resolved, lerr := s.Store.FindSSOUserByEmail(r.Context(), cfg.ID, user)
		if lerr != nil {
			apperror.Write(w, "user_not_found", apperror.ClassNotFound, "no SSO identity for that email", nil)
			return
		}
		userID = resolved
	}
	res, err := s.Store.DeprovisionSSOUser(r.Context(), teamID, userID, cfg.ID)
	if err != nil {
		apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to deprovision user", nil)
		return
	}
	uid := userID
	s.audit(r.Context(), audit.Entry{
		TeamID: teamID, ActorUserID: &actor, Source: audit.SourceUser,
		SubjectType: "membership", SubjectID: &uid, Action: ActionSSODeprovision,
		Result:  map[string]any{"membership_deactivated": res.MembershipDeactivated, "tokens_revoked": res.TokensRevoked},
		Outcome: audit.OutcomeSuccess,
	})
	writeJSON(w, http.StatusOK, dto.SSODeprovisionResponse{
		MembershipDeactivated: res.MembershipDeactivated,
		TokensRevoked:         res.TokensRevoked,
		Message:               "deprovisioned " + user,
	})
}

func (s *Service) lookupTXT(ctx context.Context, name string) ([]string, error) {
	if s.Resolver == nil {
		return nil, errors.New("no DNS resolver configured")
	}
	return s.Resolver.LookupTXT(ctx, name)
}

func containsToken(records []string, token string) bool {
	want := "0ops-verify=" + token
	for _, rec := range records {
		if strings.Contains(rec, want) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
