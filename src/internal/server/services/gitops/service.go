package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	opsruntime "github.com/winshare/zeroops/internal/shared/runtime"
)

var (
	ErrMissingRepository = errors.New("missing gitops repository")
	ErrPushConflict      = errors.New("gitops_push_conflict")
)

type service struct {
	repoURL      string
	branch       string
	worktreeRoot string
	authorName   string
	authorEmail  string
	runGit       func(context.Context, string, ...string) (string, error)
}

// NewService builds a GitOps service for the provided repo and worktree root.
func NewService(repoURL, worktreeRoot string) Service {
	return &service{
		repoURL:      strings.TrimSpace(repoURL),
		branch:       "main",
		worktreeRoot: strings.TrimSpace(worktreeRoot),
		authorName:   "ops-bot",
		authorEmail:  "ops-bot@" + opsruntime.DomainBase(),
		runGit:       runGitCommand,
	}
}

// NewServiceFromEnv builds a GitOps service from environment variables.
func NewServiceFromEnv() (Service, error) {
	repoURL := strings.TrimSpace(os.Getenv("OPS_GITOPS_REPOSITORY"))
	if repoURL == "" {
		return nil, ErrMissingRepository
	}
	worktreeRoot := strings.TrimSpace(os.Getenv("OPS_GITOPS_WORKTREE_ROOT"))
	if worktreeRoot == "" {
		worktreeRoot = filepath.Join(os.TempDir(), "0ops-gitops")
	}
	branch := strings.TrimSpace(os.Getenv("OPS_GITOPS_BRANCH"))
	svc := NewService(repoURL, worktreeRoot).(*service)
	if branch != "" {
		svc.branch = branch
	}
	if name := strings.TrimSpace(os.Getenv("OPS_GIT_AUTHOR_NAME")); name != "" {
		svc.authorName = name
	}
	if email := strings.TrimSpace(os.Getenv("OPS_GIT_AUTHOR_EMAIL")); email != "" {
		svc.authorEmail = email
	}
	return svc, nil
}

// RenderAndPush renders manifests, commits them, and pushes to gitops.
func (s *service) RenderAndPush(ctx context.Context, input RenderInput) (GitOpsResult, error) {
	if s == nil {
		return GitOpsResult{}, ErrMissingRepository
	}
	sourceCommitSHA, err := s.resolveCommitSHA(ctx, input.RepoURL, input.Ref)
	if err != nil {
		return GitOpsResult{}, err
	}
	// Image ref precedence (supply-chain-security spec § 4.4, hard rule #6):
	//  1. an explicit caller-supplied ref (already pinned upstream);
	//  2. an immutable `<repo>@sha256:<digest>` pin when the digest is known
	//     (post-build path — closes the SC3 tag-substitution window);
	//  3. a provisional `<repo>:<commit_sha>` tag only when no digest exists
	//     yet (pre-build create_app render; the GHA post-build re-render and
	//     callback supply the digest that pins the deployed manifest).
	imageRef := input.ImageRef
	if imageRef == "" {
		if pinned, ok := DigestPinnedImageRef(input.TeamSlug, input.AppSlug, input.ImageDigest); ok {
			imageRef = pinned
		} else {
			imageRef = fmt.Sprintf("ghcr.io/winshare/0ops-apps/%s/%s:%s", input.TeamSlug, input.AppSlug, sourceCommitSHA)
		}
	}
	rendered, err := s.Render(ctx, RenderInput{
		Action:      input.Action,
		TeamSlug:    input.TeamSlug,
		AppSlug:     input.AppSlug,
		RepoURL:     input.RepoURL,
		Ref:         input.Ref,
		CommitSHA:   sourceCommitSHA,
		ImageRef:    imageRef,
		PrimaryPort: input.PrimaryPort,
		DeployRunID: input.DeployRunID,
		PreviewID:   input.PreviewID,
		TraceID:     input.TraceID,
	})
	if err != nil {
		return GitOpsResult{}, err
	}
	worktreePath, err := s.ensureWorktree(ctx)
	if err != nil {
		return GitOpsResult{}, err
	}
	gitopsCommitSHA, err := s.CommitAndPush(ctx, worktreePath, CommitMessage{
		Action:      input.Action,
		TeamSlug:    input.TeamSlug,
		AppSlug:     input.AppSlug,
		DeployRunID: input.DeployRunID,
		PreviewID:   input.PreviewID,
		TraceID:     input.TraceID,
	}, rendered.Files)
	if err != nil {
		return GitOpsResult{}, err
	}
	return GitOpsResult{
		SourceCommitSHA: sourceCommitSHA,
		GitOpsCommitSHA: gitopsCommitSHA,
		ImageRef:        imageRef,
	}, nil
}

// CommitMessage formats the machine-readable commit contract.
func (s *service) CommitMessage(input CommitMessage) string {
	return input.String()
}

// CommitAndPush writes files, commits, and pushes the current worktree.
func (s *service) CommitAndPush(ctx context.Context, worktreePath string, message CommitMessage, files map[string]string) (string, error) {
	if err := s.configureGitIdentity(ctx, worktreePath); err != nil {
		return "", err
	}
	if err := s.writeFiles(worktreePath, files); err != nil {
		return "", err
	}
	if _, err := s.runGit(ctx, worktreePath, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := s.runGit(ctx, worktreePath, "commit", "-m", message.String()); err != nil && !isNothingToCommit(err) {
		return "", err
	}
	if err := s.pushWithRetry(ctx, worktreePath); err != nil {
		return "", err
	}
	head, err := s.runGit(ctx, worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(head), nil
}

func (s *service) ensureWorktree(ctx context.Context) (string, error) {
	if err := os.MkdirAll(s.worktreeRoot, 0o755); err != nil {
		return "", err
	}
	worktreePath := filepath.Join(s.worktreeRoot, "0ops-gitops")
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := s.runGit(ctx, s.worktreeRoot, "clone", s.repoURL, worktreePath); err != nil {
			return "", err
		}
		if _, err := s.runGit(ctx, worktreePath, "checkout", "-B", s.branch); err != nil {
			return "", err
		}
		return worktreePath, nil
	} else if err != nil {
		return "", err
	}
	if _, err := s.runGit(ctx, worktreePath, "fetch", "origin", s.branch); err != nil {
		return "", err
	}
	if _, err := s.runGit(ctx, worktreePath, "checkout", "-B", s.branch, "origin/"+s.branch); err != nil {
		return "", err
	}
	if _, err := s.runGit(ctx, worktreePath, "pull", "--rebase", "origin", s.branch); err != nil {
		return "", err
	}
	return worktreePath, nil
}

func (s *service) configureGitIdentity(ctx context.Context, worktreePath string) error {
	if _, err := s.runGit(ctx, worktreePath, "config", "user.name", s.authorName); err != nil {
		return err
	}
	if _, err := s.runGit(ctx, worktreePath, "config", "user.email", s.authorEmail); err != nil {
		return err
	}
	return nil
}

func (s *service) writeFiles(worktreePath string, files map[string]string) error {
	for path, contents := range files {
		full := filepath.Join(worktreePath, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) pushWithRetry(ctx context.Context, worktreePath string) error {
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := s.runGit(ctx, worktreePath, "push", "origin", s.branch); err == nil {
			return nil
		} else if !isNonFastForward(err) {
			return err
		}
		if _, err := s.runGit(ctx, worktreePath, "fetch", "origin", s.branch); err != nil {
			return err
		}
		if _, err := s.runGit(ctx, worktreePath, "rebase", "origin/"+s.branch); err != nil {
			return err
		}
	}
	return ErrPushConflict
}

func (s *service) resolveCommitSHA(ctx context.Context, repoURL, ref string) (string, error) {
	if strings.TrimSpace(repoURL) == "" {
		return "", ErrMissingRepository
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	out, err := s.runGit(ctx, "", "ls-remote", repoURL, ref)
	if err == nil {
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) > 0 {
			return fields[0], nil
		}
	}
	if len(ref) >= 7 {
		return ref, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("unable to resolve commit sha for %s@%s", repoURL, ref)
}

func runGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func isNonFastForward(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "fetch first") || strings.Contains(msg, "rejected")
}

func isNothingToCommit(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nothing to commit") || strings.Contains(msg, "nothing added to commit")
}
