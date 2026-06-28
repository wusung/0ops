package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDigestPinnedImageRef covers the pure digest-pin builder
// (supply-chain-security spec § 4.4 / hard rule #6).
func TestDigestPinnedImageRef(t *testing.T) {
	const hex = "abc123def4567890abc123def4567890abc123def4567890abc123def4567890"
	cases := []struct {
		name   string
		digest string
		want   string
		ok     bool
	}{
		{"bare hex", hex, "ghcr.io/winshare/0ops-apps/acme/web@sha256:" + hex, true},
		{"sha256 prefixed", "sha256:" + hex, "ghcr.io/winshare/0ops-apps/acme/web@sha256:" + hex, true},
		{"surrounding space", "  sha256:" + hex + "  ", "ghcr.io/winshare/0ops-apps/acme/web@sha256:" + hex, true},
		{"empty", "", "", false},
		{"mutable tag rejected", "v1.2.3", "", false},
		{"short", "abc123", "", false},
		{"uppercase hex rejected", strings.ToUpper(hex), "", false},
		{"non-hex char", strings.Repeat("g", 64), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DigestPinnedImageRef("acme", "web", tc.digest)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got=%q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Fatalf("ref = %q, want %q", got, tc.want)
			}
			if ok && !strings.Contains(got, "@sha256:") {
				t.Fatalf("pinned ref %q is not an immutable digest pin", got)
			}
		})
	}
}

// TestRenderAndPushPinsDigestWhenProvided asserts that when the build digest
// is known, the rendered manifest pins an immutable `@sha256:` digest and does
// NOT fall back to a mutable `:<commit_sha>` tag (spec hard rule #6, SC3).
func TestRenderAndPushPinsDigestWhenProvided(t *testing.T) {
	const digest = "1111111111111111111111111111111111111111111111111111111111111111"
	sourceRepo := initGitRepo(t, "source", "main", "README.md", "source\n")
	gitopsRepo := initBareRepo(t, "gitops")
	worktreeRoot := t.TempDir()

	svc := NewService(gitopsRepo, worktreeRoot).(*service)
	svc.runGit = runGitCommand

	result, err := svc.RenderAndPush(context.Background(), RenderInput{
		Action:      "create_app",
		TeamSlug:    "acme",
		AppSlug:     "web",
		RepoURL:     sourceRepo,
		Ref:         "main",
		ImageDigest: "sha256:" + digest,
		DeployRunID: "deploy-1",
		PreviewID:   "preview-1",
		TraceID:     "trace-1",
		PrimaryPort: 3000,
	})
	if err != nil {
		t.Fatalf("RenderAndPush() error = %v", err)
	}
	wantRef := "ghcr.io/winshare/0ops-apps/acme/web@sha256:" + digest
	if result.ImageRef != wantRef {
		t.Fatalf("ImageRef = %q, want %q", result.ImageRef, wantRef)
	}
	if strings.Contains(result.ImageRef, ":"+result.SourceCommitSHA) {
		t.Fatalf("image ref %q fell back to a mutable commit-sha tag", result.ImageRef)
	}

	cloned := filepath.Join(t.TempDir(), "clone")
	if _, err := runGitCommand(context.Background(), "", "clone", gitopsRepo, cloned); err != nil {
		t.Fatalf("clone gitops repo: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cloned, "apps", "acme", "web", "deployment.yaml"))
	if err != nil {
		t.Fatalf("read deployment.yaml: %v", err)
	}
	if !strings.Contains(string(data), "@sha256:"+digest) {
		t.Fatalf("deployment manifest is not digest-pinned: %s", string(data))
	}
}

// TestClassifyCommit covers the non-backend-commit detective control
// (supply-chain-security spec § 7.2 / hard rule #8).
func TestClassifyCommit(t *testing.T) {
	expected := ExpectedSigner{
		AuthorEmail:    "ops-bot@jesontech.com",
		SignerIdentity: "ops-bot@jesontech.com",
	}
	good := CommitMeta{
		AuthorEmail:    "ops-bot@jesontech.com",
		SignerIdentity: "ops-bot@jesontech.com",
		GoodSignature:  true,
		Message:        "create_app: acme/web @ deploy-123\n\nTrace-Id: t1\n",
	}

	cases := []struct {
		name       string
		mutate     func(*CommitMeta)
		authorized bool
		reason     string
	}{
		{"authorized backend commit", func(*CommitMeta) {}, true, ""},
		{"unsigned", func(m *CommitMeta) { m.GoodSignature = false }, false, "unsigned_or_bad_signature"},
		{"wrong signer", func(m *CommitMeta) { m.SignerIdentity = "attacker@evil.com" }, false, "signer_not_ops_bot"},
		{"empty signer", func(m *CommitMeta) { m.SignerIdentity = "" }, false, "signer_not_ops_bot"},
		{"wrong author", func(m *CommitMeta) { m.AuthorEmail = "human@example.com" }, false, "author_not_ops_bot"},
		{"missing deploy_run_id", func(m *CommitMeta) { m.Message = "hand-edited the manifest" }, false, "missing_deploy_run_id"},
		{"empty deploy_run_id", func(m *CommitMeta) { m.Message = "create_app: acme/web @ \n" }, false, "missing_deploy_run_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := good
			tc.mutate(&meta)
			authorized, reason := ClassifyCommit(meta, expected)
			if authorized != tc.authorized || reason != tc.reason {
				t.Fatalf("ClassifyCommit() = (%v, %q), want (%v, %q)", authorized, reason, tc.authorized, tc.reason)
			}
		})
	}
}

func TestParseDeployRunID(t *testing.T) {
	cases := []struct {
		message string
		want    string
		ok      bool
	}{
		{"create_app: acme/web @ deploy-9\n\nTrace-Id: t\n", "deploy-9", true},
		{"redeploy: t/a @ run-1", "run-1", true},
		{"no contract here", "", false},
		{"action: t/a @ ", "", false},
	}
	for _, tc := range cases {
		got, ok := parseDeployRunID(tc.message)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("parseDeployRunID(%q) = (%q, %v), want (%q, %v)", tc.message, got, ok, tc.want, tc.ok)
		}
	}
}
