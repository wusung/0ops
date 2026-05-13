package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitMessageString(t *testing.T) {
	got := CommitMessage{
		Action:      "create_app",
		TeamSlug:    "acme",
		AppSlug:     "nextdemo",
		DeployRunID: "deploy-1",
		PreviewID:   "preview-1",
		TraceID:     "trace-1",
	}.String()

	want := "create_app: acme/nextdemo @ deploy-1\n\nPreview-Id: preview-1\nTrace-Id: trace-1\n"
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestRenderAndPushCreatesGitOpsCommit(t *testing.T) {
	sourceRepo := initGitRepo(t, "source", "main", "README.md", "source\n")
	gitopsRepo := initBareRepo(t, "gitops")
	worktreeRoot := t.TempDir()

	svc := NewService(gitopsRepo, worktreeRoot).(*service)
	svc.runGit = runGitCommand

	result, err := svc.RenderAndPush(context.Background(), RenderInput{
		Action:      "create_app",
		TeamSlug:    "acme",
		AppSlug:     "nextdemo",
		RepoURL:     sourceRepo,
		Ref:         "main",
		DeployRunID: "deploy-1",
		PreviewID:   "preview-1",
		TraceID:     "trace-1",
		PrimaryPort: 3000,
	})
	if err != nil {
		t.Fatalf("RenderAndPush() error = %v", err)
	}
	if result.SourceCommitSHA == "" || result.GitOpsCommitSHA == "" {
		t.Fatalf("missing commit shas: %#v", result)
	}
	if !strings.Contains(result.ImageRef, result.SourceCommitSHA) {
		t.Fatalf("image ref %q does not include source commit sha %q", result.ImageRef, result.SourceCommitSHA)
	}

	cloned := filepath.Join(t.TempDir(), "clone")
	if _, err := runGitCommand(context.Background(), "", "clone", gitopsRepo, cloned); err != nil {
		t.Fatalf("clone gitops repo: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cloned, "apps", "acme", "nextdemo", "deployment.yaml"))
	if err != nil {
		t.Fatalf("read deployment.yaml: %v", err)
	}
	if !strings.Contains(string(data), result.ImageRef) {
		t.Fatalf("deployment missing image ref %q: %s", result.ImageRef, string(data))
	}
	if _, err := os.Stat(filepath.Join(cloned, "teams", "acme", "namespace.yaml")); err != nil {
		t.Fatalf("namespace.yaml missing: %v", err)
	}
}

func TestPushWithRetryRetriesNonFastForward(t *testing.T) {
	svc := &service{
		branch: "main",
	}
	var calls []string
	svc.runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case strings.HasPrefix(call, "push"):
			if len(calls) == 1 {
				return "", errors.New("rejected non-fast-forward")
			}
			return "", nil
		default:
			return "", nil
		}
	}
	if err := svc.pushWithRetry(context.Background(), "/tmp/worktree"); err != nil {
		t.Fatalf("pushWithRetry() error = %v", err)
	}
	if got := strings.Join(calls, "\n"); !strings.Contains(got, "fetch origin main") || !strings.Contains(got, "rebase origin/main") {
		t.Fatalf("retry path not exercised: %s", got)
	}
}

func initGitRepo(t *testing.T, name, branch, fileName, contents string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mustGit(t, "", "init", "-b", branch, dir)
	mustGit(t, dir, "config", "user.name", "tester")
	mustGit(t, dir, "config", "user.email", "tester@example.com")
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(contents), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "init")
	return dir
}

func initBareRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mustGit(t, "", "init", "--bare", dir)
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitCommand(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}
