package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/shared/authconfig"
	"github.com/winshare/zeroops/internal/shared/dto"
)

func TestSSOStatusRendersTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sso") || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.SSOStatus{
			Protocol:       "oidc",
			Issuer:         "https://acme.okta.com",
			Enforce:        true,
			JITDefaultRole: "member",
			PATPolicy:      "disallow",
			Domains:        []dto.SSODomainStatus{{Domain: "acme.com", Verified: true}},
		})
	}))
	t.Cleanup(srv.Close)
	cfg, _ := authconfig.Load()
	t.Cleanup(func() { _ = authconfig.Save(cfg) })

	root := NewRootCommand()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"sso", "status", "--team", "acme", "--host", srv.URL, "--token", "fake", "--output", "table"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "oidc") || !strings.Contains(got, "acme.com(✓)") || !strings.Contains(got, "disallow") {
		t.Fatalf("status table missing fields:\n%s", got)
	}
}

func TestSSODeprovisionPostsUserAndRenders(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sso/deprovision") || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dto.SSODeprovisionResponse{
			MembershipDeactivated: true, TokensRevoked: 3, Message: "deprovisioned bob@acme.com",
		})
	}))
	t.Cleanup(srv.Close)
	cfg, _ := authconfig.Load()
	t.Cleanup(func() { _ = authconfig.Save(cfg) })

	root := NewRootCommand()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"sso", "deprovision", "--user", "bob@acme.com", "--yes",
		"--team", "acme", "--host", srv.URL, "--token", "fake", "--output", "table"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var req dto.SSODeprovisionRequest
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.User != "bob@acme.com" {
		t.Fatalf("posted user = %q", req.User)
	}
	if !strings.Contains(out.String(), "3 tokens revoked") {
		t.Fatalf("deprovision output missing summary:\n%s", out.String())
	}
}

func TestSSODeprovisionRequiresUser(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"sso", "deprovision", "--yes", "--team", "acme", "--host", "http://127.0.0.1:1", "--token", "fake"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when --user is missing")
	}
}
