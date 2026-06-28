package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/db"
)

// newDeleteCapableStore returns a CLI fake store whose default token has
// the apps:delete scope and admin role (spec § 13 hard rule #2).
func newDeleteCapableStore(t *testing.T) (*cliFakeStore, string) {
	t.Helper()
	store, token := newCLIFakeStore()
	store.role = "admin"
	store.token.Scopes = []string{"apps:read", "apps:write", "apps:delete", "teams:read", "members:manage"}
	store.tokens[store.token.ID] = store.token
	live := "live"
	store.apps[0].Status = &live
	return store, token
}

func TestAppsDeleteRefusesWhenSlugTypedIncorrectly(t *testing.T) {
	store, token := newDeleteCapableStore(t)
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "delete", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetIn(strings.NewReader("nope\n"))

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Fatalf("expected slug mismatch abort, got %v", err)
	}
	if got := store.apps[0].Status; got == nil || *got != "live" {
		t.Fatalf("app status mutated despite abort: %v", got)
	}
	if !strings.Contains(stdout.String(), "irreversible") {
		t.Fatalf("expected warning banner; got %q", stdout.String())
	}
}

func TestAppsDeleteYesStillRequiresTypedSlug(t *testing.T) {
	store, token := newDeleteCapableStore(t)
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "delete", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--yes"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetIn(strings.NewReader("")) // empty → slug mismatch

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "slug mismatch") {
		t.Fatalf("expected --yes to still require slug typing, got %v", err)
	}
}

func TestAppsDeleteHappyPath(t *testing.T) {
	store, token := newDeleteCapableStore(t)
	store.domains = []db.DomainBinding{{ID: "d1", TeamID: store.team.ID, AppID: "1", AppSlug: "alpha", Hostname: "alpha.jesontech.com", Kind: strPtr("primary")}}
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "delete", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--yes"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	// slug guard, then the backend-generated high-risk phrase.
	cmd.SetIn(strings.NewReader("alpha\nDELETE alpha\n"))

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v\noutput: %s", err, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "RISK: critical") {
		t.Fatalf("expected RISK header, got %q", out)
	}
	if !strings.Contains(out, "deletion initiated") {
		t.Fatalf("expected confirmation line, got %q", out)
	}
	if !strings.Contains(out, "irreversible") {
		t.Fatalf("expected irreversible warning, got %q", out)
	}
}

func TestAppsDeleteSecondPromptAcceptsNo(t *testing.T) {
	store, token := newDeleteCapableStore(t)
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "delete", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetIn(strings.NewReader("alpha\nDELETE alpha\nN\n"))

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := store.apps[0].Status; got == nil || *got != "live" {
		t.Fatalf("app status mutated despite N reply: %v", got)
	}
}

// spec § 5.4: typing the wrong high-risk phrase aborts locally before any
// confirm request is sent.
func TestAppsDeleteRefusesWhenPhraseTypedIncorrectly(t *testing.T) {
	store, token := newDeleteCapableStore(t)
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"apps", "delete", "alpha", "--team", store.team.Slug, "--host", srv.URL, "--token", token, "--yes"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	// correct slug guard, then a wrong high-risk phrase.
	cmd.SetIn(strings.NewReader("alpha\nDELETE wrong\n"))

	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "confirmation phrase mismatch") {
		t.Fatalf("expected confirmation phrase mismatch abort, got %v", err)
	}
	if got := store.apps[0].Status; got == nil || *got != "live" {
		t.Fatalf("app status mutated despite phrase mismatch: %v", got)
	}
}
