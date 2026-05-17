package createapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@local", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@local", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestLocalInspector_Node(t *testing.T) {
	dir := setupGitRepo(t, map[string]string{
		"package.json": `{"name":"x","engines":{"node":"20.x"}}`,
	})
	t.Setenv("LOCAL_FILE_REPO_ROOT", dir)
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")

	got, err := LocalInspector{}.Inspect(context.Background(), "file://"+dir, "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got.DefaultBranch != "main" {
		t.Errorf("default_branch=%q want main", got.DefaultBranch)
	}
	if got.Builder != "paketobuildpacks/builder-jammy-base" {
		t.Errorf("builder=%q", got.Builder)
	}
	if got.PrimaryPort != 3000 {
		t.Errorf("port=%d want 3000", got.PrimaryPort)
	}
	if len(got.CommitSHA) != 40 {
		t.Errorf("commit_sha=%q must be 40 hex", got.CommitSHA)
	}
	if got.GitHubAppStatus != "not_applicable" {
		t.Errorf("github status=%q", got.GitHubAppStatus)
	}
}

func TestLocalInspector_Go(t *testing.T) {
	dir := setupGitRepo(t, map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.22\n",
	})
	t.Setenv("LOCAL_FILE_REPO_ROOT", dir)
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")

	got, err := LocalInspector{}.Inspect(context.Background(), "file://"+dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.PrimaryPort != 8080 {
		t.Errorf("port=%d want 8080", got.PrimaryPort)
	}
}

func TestLocalInspector_DetectFailed(t *testing.T) {
	dir := setupGitRepo(t, map[string]string{"README.md": "no code"})
	t.Setenv("LOCAL_FILE_REPO_ROOT", dir)
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	_, err := LocalInspector{}.Inspect(context.Background(), "file://"+dir, "")
	if !errors.Is(err, ErrBuildpackDetectFailed) {
		t.Fatalf("err=%v want ErrBuildpackDetectFailed", err)
	}
}

func TestLocalInspector_GateOff(t *testing.T) {
	dir := setupGitRepo(t, map[string]string{
		"package.json": `{"name":"x"}`,
	})
	t.Setenv("LOCAL_FILE_REPO_ROOT", dir)
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	_, err := LocalInspector{}.Inspect(context.Background(), "file://"+dir, "")
	if !errors.Is(err, ErrLocalFileRepoDisabled) {
		t.Fatalf("err=%v want ErrLocalFileRepoDisabled", err)
	}
}
