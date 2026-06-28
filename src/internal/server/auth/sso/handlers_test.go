package sso

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// fakeStore implements auth.Store + sso.Store for handler tests.
type fakeStore struct {
	tokenRow  db.CliToken
	teamID    string
	teamSlug  string
	role      string
	cfg       *db.IdPConfig
	cfgErr    error
	hasDomain bool
	domainVer bool
	addErr    error
	domains   []db.IdPDomain
	domain    db.IdPDomain
	jit       db.JITResult
	deprov    db.DeprovisionResult
	depUser   string
	issued    string
}

func (f *fakeStore) FindCliTokenByID(_ context.Context, id string) (db.CliToken, error) {
	if id == f.tokenRow.ID {
		return f.tokenRow, nil
	}
	return db.CliToken{}, pgx.ErrNoRows
}
func (f *fakeStore) ResolveTeamBySlug(_ context.Context, slug string) (db.Team, error) {
	if slug == f.teamSlug {
		return db.Team{ID: f.teamID, Slug: f.teamSlug, Name: "Acme", Plan: "team"}, nil
	}
	return db.Team{ID: "other-team-id", Slug: slug, Name: "Other", Plan: "team"}, nil
}
func (f *fakeStore) CheckTeamMembership(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}
func (f *fakeStore) GetTeamMembershipRole(_ context.Context, _, _ string) (string, error) {
	return f.role, nil
}
func (f *fakeStore) CreateIdPConfig(_ context.Context, p db.CreateIdPConfigParams) (db.IdPConfig, error) {
	if f.cfgErr != nil {
		return db.IdPConfig{}, f.cfgErr
	}
	return db.IdPConfig{ID: "cfg-1", TeamID: p.TeamID, Protocol: "oidc", Issuer: p.Issuer, ClientID: p.ClientID, ClientSecretRef: p.ClientSecretRef, JITDefaultRole: "member", PATPolicy: "allow"}, nil
}
func (f *fakeStore) GetIdPConfigByTeam(_ context.Context, _ string) (db.IdPConfig, error) {
	if f.cfg == nil {
		return db.IdPConfig{}, pgx.ErrNoRows
	}
	return *f.cfg, nil
}
func (f *fakeStore) UpdateIdPConfig(_ context.Context, _ string, patch db.IdPConfigPatch) (db.IdPConfig, error) {
	c := *f.cfg
	if patch.Enforce != nil {
		c.Enforce = *patch.Enforce
	}
	if patch.PATPolicy != nil {
		c.PATPolicy = *patch.PATPolicy
	}
	return c, nil
}
func (f *fakeStore) DeleteIdPConfig(_ context.Context, _ string) error { return nil }
func (f *fakeStore) AddIdPDomain(_ context.Context, _, _, domain, tkn string) (db.IdPDomain, error) {
	if f.addErr != nil {
		return db.IdPDomain{}, f.addErr
	}
	return db.IdPDomain{ID: "dom-1", Domain: domain, VerificationToken: tkn, TeamID: f.teamID, IdPConfigID: "cfg-1"}, nil
}
func (f *fakeStore) GetIdPDomain(_ context.Context, _ string) (db.IdPDomain, error) {
	return f.domain, nil
}
func (f *fakeStore) ListIdPDomains(_ context.Context, _ string) ([]db.IdPDomain, error) {
	return f.domains, nil
}
func (f *fakeStore) MarkIdPDomainVerified(_ context.Context, _ string) (db.IdPDomain, error) {
	now := time.Now()
	d := f.domain
	d.Verified = true
	d.VerifiedAt = &now
	return d, nil
}
func (f *fakeStore) FindSSOUserByEmail(_ context.Context, _, _ string) (string, error) {
	return "user-resolved", nil
}
func (f *fakeStore) HasVerifiedDomain(_ context.Context, _ string) (bool, error) {
	return f.hasDomain, nil
}
func (f *fakeStore) IsDomainVerifiedForConfig(_ context.Context, _, _ string) (bool, error) {
	return f.domainVer, nil
}
func (f *fakeStore) JITProvision(_ context.Context, _ db.JITParams) (db.JITResult, error) {
	return f.jit, nil
}
func (f *fakeStore) IssueSSOToken(_ context.Context, _, _, _ string, _ []string, _ time.Time) (string, error) {
	if f.issued == "" {
		return "op_dev_issued.secret", nil
	}
	return f.issued, nil
}
func (f *fakeStore) DeprovisionSSOUser(_ context.Context, _, _, _ string) (db.DeprovisionResult, error) {
	return f.deprov, nil
}
func (f *fakeStore) DeprovisionSSOUserBySubject(_ context.Context, _, _, _ string) (db.DeprovisionResult, string, error) {
	return f.deprov, f.depUser, nil
}

type captureAudit struct{ entries []audit.Entry }

func (c *captureAudit) Log(_ context.Context, e audit.Entry) error {
	c.entries = append(c.entries, e)
	return nil
}
func (c *captureAudit) has(action string, outcome audit.Outcome) bool {
	for _, e := range c.entries {
		if e.Action == action && e.Outcome == outcome {
			return true
		}
	}
	return false
}

type stubExchanger struct{ idToken string }

func (s stubExchanger) Exchange(_ context.Context, _ Discovery, _, _, _, _, _ string) (string, time.Time, error) {
	return s.idToken, time.Time{}, nil
}

func newServer(t *testing.T, f *fakeStore, svc *Service) (*httptest.Server, string) {
	t.Helper()
	bearer, err := auth.NewBearerToken("device", "tok-1")
	if err != nil {
		t.Fatalf("NewBearerToken: %v", err)
	}
	parsed, _ := auth.ParseBearerToken(bearer)
	hash := auth.HashBearerToken(parsed.Secret)
	f.tokenRow = db.CliToken{ID: "tok-1", OwnerUserID: "owner-1", TeamID: f.teamID, TokenHash: hash, Scopes: []string{"sso:manage"}}

	mw := auth.NewMiddleware(f)
	r := chi.NewRouter()
	r.Route("/v1/auth", func(sr chi.Router) {
		RegisterAuthRoutes(sr, svc)
	})
	r.Route("/v1/teams/{team_slug}", func(sr chi.Router) {
		sr.Use(mw.Bearer)
		sr.Use(mw.ResolveTeam)
		sr.Use(mw.CheckMembership)
		RegisterTeamRoutes(sr, mw, svc)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, bearer
}

func newSvc(f *fakeStore, verifier IDTokenVerifier, exch CodeExchanger, state StateStore) (*Service, *captureAudit) {
	au := &captureAudit{}
	secrets := NewMemorySecretStore()
	_ = secrets.Put("sso/team-acme/client_secret", "shh")
	if state == nil {
		state = NewMemoryStateStore()
	}
	return NewService(f, au, verifier, exch, secrets, state, nil, "https://api.test"), au
}

func do(t *testing.T, method, url, bearer, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	switch {
	case strings.HasPrefix(body, "{"):
		req.Header.Set("Content-Type", "application/json")
	case body != "":
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return res
}

func baseFake() *fakeStore {
	return &fakeStore{teamID: "team-acme", teamSlug: "acme", role: "owner"}
}

func jwtClaims(iss, aud, sub, email string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": iss, "aud": aud, "sub": sub, "email": email, "email_verified": true,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	}
}

func logoutClaims(iss, aud, sub string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": iss, "aud": aud, "sub": sub, "iat": time.Now().Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	}
}

func TestOwnerCreatesConfigNoSecretEcho(t *testing.T) {
	f := baseFake()
	svc, au := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)

	res := do(t, http.MethodPost, srv.URL+"/v1/teams/acme/sso", bearer,
		`{"issuer":"https://idp.example.com","client_id":"cid","client_secret":"shh"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if _, leaked := body["client_secret"]; leaked {
		t.Fatal("response must not echo client_secret")
	}
	if !au.has(ActionSSOConfigCreate, audit.OutcomeSuccess) {
		t.Fatal("expected sso_config_create audit")
	}
	for _, e := range au.entries {
		if m, ok := e.Args.(map[string]any); ok {
			if _, bad := m["client_secret"]; bad {
				t.Fatal("client_secret must never enter audit args")
			}
		}
	}
}

func TestMemberCannotWriteSSO(t *testing.T) {
	f := baseFake()
	f.role = "member"
	svc, _ := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)
	res := do(t, http.MethodPost, srv.URL+"/v1/teams/acme/sso", bearer,
		`{"issuer":"https://idp.example.com","client_id":"cid","client_secret":"shh"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member write status = %d, want 403", res.StatusCode)
	}
}

func TestAdminCanReadButNotWriteSSO(t *testing.T) {
	f := baseFake()
	f.role = "admin"
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: "https://idp", JITDefaultRole: "member", PATPolicy: "allow"}
	svc, _ := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)

	if res := do(t, http.MethodGet, srv.URL+"/v1/teams/acme/sso", bearer, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("admin read status = %d, want 200", res.StatusCode)
	}
	if res := do(t, http.MethodDelete, srv.URL+"/v1/teams/acme/sso", bearer, ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("admin delete status = %d, want 403", res.StatusCode)
	}
}

func TestCrossTeamRequestIs404(t *testing.T) {
	f := baseFake()
	svc, _ := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)
	res := do(t, http.MethodGet, srv.URL+"/v1/teams/other/sso", bearer, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-team status = %d, want 404", res.StatusCode)
	}
}

func TestEnforceRequiresVerifiedDomain(t *testing.T) {
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: "https://idp", JITDefaultRole: "member", PATPolicy: "allow"}
	f.hasDomain = false
	svc, _ := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)
	res := do(t, http.MethodPatch, srv.URL+"/v1/teams/acme/sso", bearer, `{"enforce":true}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("enforce-without-domain status = %d, want 400", res.StatusCode)
	}
}

func TestAddDomainConflict(t *testing.T) {
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", PATPolicy: "allow"}
	f.addErr = db.ErrDomainTaken
	svc, _ := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)
	res := do(t, http.MethodPost, srv.URL+"/v1/teams/acme/sso/domains", bearer, `{"domain":"acme.com"}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("domain-taken status = %d, want 409", res.StatusCode)
	}
}

func TestOwnerDeprovision(t *testing.T) {
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", PATPolicy: "allow"}
	f.deprov = db.DeprovisionResult{MembershipDeactivated: true, TokensRevoked: 3}
	svc, au := newSvc(f, nil, nil, nil)
	srv, bearer := newServer(t, f, svc)
	res := do(t, http.MethodPost, srv.URL+"/v1/teams/acme/sso/deprovision", bearer, `{"user":"bob@acme.com"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("deprovision status = %d, want 200", res.StatusCode)
	}
	var body dto.SSODeprovisionResponse
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.TokensRevoked != 3 || !body.MembershipDeactivated {
		t.Fatalf("deprovision body = %+v", body)
	}
	if !au.has(ActionSSODeprovision, audit.OutcomeSuccess) {
		t.Fatal("expected sso_deprovision_user audit")
	}
}

func TestCallbackJITAndAudit(t *testing.T) {
	idp := newMockIdP(t)
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: idp.issuer, ClientID: "cid", ClientSecretRef: "sso/team-acme/client_secret", JITDefaultRole: "member", PATPolicy: "allow", SessionMaxTTLS: 28800}
	f.domainVer = true
	f.jit = db.JITResult{UserID: "u-new", Role: "member", ProvisionedMembership: true}

	idToken := idp.signIDToken(t, jwtClaims(idp.issuer, "cid", "sub-x", "alice@acme.com"))
	verifier := NewVerifier(idp.server.Client())
	state := NewMemoryStateStore()
	state.Save("st-1", StateData{TeamSlug: "acme", CodeVerifier: "v"})
	svc, au := newSvc(f, verifier, stubExchanger{idToken: idToken}, state)
	srv, _ := newServer(t, f, svc)

	res := do(t, http.MethodGet, srv.URL+"/v1/auth/sso/acme/callback?code=abc&state=st-1", "", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", res.StatusCode)
	}
	var body dto.SSOCallbackResponse
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.BearerToken == "" || body.Email != "alice@acme.com" {
		t.Fatalf("callback body = %+v", body)
	}
	if !au.has(ActionSSOLogin, audit.OutcomeSuccess) || !au.has(ActionSSOProvisionUser, audit.OutcomeSuccess) {
		t.Fatal("expected sso_login + sso_provision_user audit")
	}
}

func TestCallbackDomainMismatch(t *testing.T) {
	idp := newMockIdP(t)
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: idp.issuer, ClientID: "cid", ClientSecretRef: "sso/team-acme/client_secret", JITDefaultRole: "member", PATPolicy: "allow", SessionMaxTTLS: 28800}
	f.domainVer = false

	idToken := idp.signIDToken(t, jwtClaims(idp.issuer, "cid", "sub-x", "mallory@evil.example"))
	verifier := NewVerifier(idp.server.Client())
	state := NewMemoryStateStore()
	state.Save("st-2", StateData{TeamSlug: "acme", CodeVerifier: "v"})
	svc, au := newSvc(f, verifier, stubExchanger{idToken: idToken}, state)
	srv, _ := newServer(t, f, svc)

	res := do(t, http.MethodGet, srv.URL+"/v1/auth/sso/acme/callback?code=abc&state=st-2", "", "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("domain-mismatch status = %d, want 403", res.StatusCode)
	}
	if !au.has(ActionSSOLogin, audit.OutcomeFailure) {
		t.Fatal("expected sso_login failure audit")
	}
}

func TestCallbackRejectsUnverifiedEmail(t *testing.T) {
	idp := newMockIdP(t)
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: idp.issuer, ClientID: "cid", ClientSecretRef: "sso/team-acme/client_secret", JITDefaultRole: "member", PATPolicy: "allow", SessionMaxTTLS: 28800}
	f.domainVer = true

	// email present but email_verified=false → must be rejected before binding.
	idToken := idp.signIDToken(t, jwt.MapClaims{
		"iss": idp.issuer, "aud": "cid", "sub": "sub-x", "email": "alice@acme.com", "email_verified": false,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	verifier := NewVerifier(idp.server.Client())
	state := NewMemoryStateStore()
	state.Save("st-3", StateData{TeamSlug: "acme", CodeVerifier: "v"})
	svc, _ := newSvc(f, verifier, stubExchanger{idToken: idToken}, state)
	srv, _ := newServer(t, f, svc)

	res := do(t, http.MethodGet, srv.URL+"/v1/auth/sso/acme/callback?code=abc&state=st-3", "", "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("unverified-email status = %d, want 403", res.StatusCode)
	}
}

func TestBackchannelLogoutDeprovisions(t *testing.T) {
	idp := newMockIdP(t)
	f := baseFake()
	f.cfg = &db.IdPConfig{ID: "cfg-1", TeamID: "team-acme", Protocol: "oidc", Issuer: idp.issuer, ClientID: "cid", ClientSecretRef: "sso/team-acme/client_secret", PATPolicy: "allow"}
	f.deprov = db.DeprovisionResult{MembershipDeactivated: true, TokensRevoked: 2}
	f.depUser = "u-bob"

	logoutToken := idp.signIDToken(t, logoutClaims(idp.issuer, "cid", "sub-bob"))
	verifier := NewVerifier(idp.server.Client())
	svc, au := newSvc(f, verifier, nil, nil)
	srv, _ := newServer(t, f, svc)

	res := do(t, http.MethodPost, srv.URL+"/v1/auth/sso/acme/backchannel-logout", "",
		"logout_token="+logoutToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("backchannel status = %d, want 200", res.StatusCode)
	}
	var found bool
	for _, e := range au.entries {
		if e.Action == ActionSSOLogout {
			found = true
			if e.Source != audit.SourceSystem || e.ActorUserID != nil {
				t.Fatalf("logout audit source=%q actor=%v; want system/nil", e.Source, e.ActorUserID)
			}
		}
	}
	if !found {
		t.Fatal("expected sso_logout audit")
	}
}
