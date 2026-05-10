package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	serverpkg "github.com/winshare/zeroops/internal/server"
	"net/http/httptest"
)

func TestTeamsListCommand(t *testing.T) {
	store, token := newCLIFakeStore()
	srv := httptest.NewServer(serverpkg.NewRouter(store))
	t.Cleanup(srv.Close)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"teams", "list",
		"--host", srv.URL,
		"--token", token,
		"--output", "json",
	})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected items payload: %#v", out["items"])
	}
}

func TestTeamsUseCommandUpdatesDefaultTeamSlug(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path := filepath.Join(dir, "0ops")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	data := []byte(`{"version":1,"tokens":[{"host":"https://api.example.test","default_team_slug":"old-team","bearer_token":"token"}]}`)
	if err := os.WriteFile(filepath.Join(path, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"teams", "use", "new-team", "--host", "https://api.example.test"})

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	updated, err := os.ReadFile(filepath.Join(path, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(updated, &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	tokens := out["tokens"].([]any)
	entry := tokens[0].(map[string]any)
	if entry["default_team_slug"] != "new-team" {
		t.Fatalf("default_team_slug = %#v, want %q", entry["default_team_slug"], "new-team")
	}
}
