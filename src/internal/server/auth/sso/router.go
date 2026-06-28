package sso

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/shared/rbac"
)

// RegisterTeamRoutes mounts the team-scoped SSO endpoints onto a chi.Router that
// already carries the Bearer → ResolveTeam → CheckMembership chain. Each route
// adds its own scope gate (writes owner-only, reads admin; spec § 12).
func RegisterTeamRoutes(sr chi.Router, mw *auth.Middleware, svc *Service) {
	manage := func(h http.HandlerFunc) http.Handler {
		return mw.CheckTokenScope(rbac.ActionManageSSO, h)
	}
	read := func(h http.HandlerFunc) http.Handler {
		return mw.CheckTokenScope(rbac.ActionReadSSO, h)
	}
	sr.Method(http.MethodPost, "/sso", manage(svc.CreateConfig))
	sr.Method(http.MethodGet, "/sso", read(svc.GetStatus))
	sr.Method(http.MethodPatch, "/sso", manage(svc.UpdateConfig))
	sr.Method(http.MethodDelete, "/sso", manage(svc.DeleteConfig))
	sr.Method(http.MethodPost, "/sso/domains", manage(svc.AddDomain))
	sr.Method(http.MethodPost, "/sso/domains/{domain_id}/verify", manage(svc.VerifyDomain))
	sr.Method(http.MethodPost, "/sso/deprovision", manage(svc.Deprovision))
}

// RegisterAuthRoutes mounts the unauthenticated IdP-facing endpoints onto the
// /v1/auth subrouter (paths are relative to /v1/auth). They sit outside the
// Bearer-protected team subtree because the IdP redirect / back-channel carry
// no 0ops token; each validates state+PKCE or the IdP signature itself
// (spec § 12). Resulting paths: /v1/auth/sso/{team_slug}/callback and
// /v1/auth/sso/{team_slug}/backchannel-logout.
func RegisterAuthRoutes(sr chi.Router, svc *Service) {
	sr.Get("/sso/{team_slug}/callback", svc.Callback)
	sr.Post("/sso/{team_slug}/backchannel-logout", svc.BackchannelLogout)
}
