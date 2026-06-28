package gitops

import "context"

// RenderInput describes a manifest render request.
type RenderInput struct {
	Action      string
	TeamSlug    string
	AppSlug     string
	RepoURL     string
	Ref         string
	CommitSHA   string
	ImageRef    string
	ImageDigest string
	PrimaryPort int
	DeployRunID string
	PreviewID   string
	TraceID     string
}

// RenderResult contains the rendered files.
type RenderResult struct {
	Files map[string]string
}

// GitOpsResult describes the output of a render/push run.
type GitOpsResult struct {
	SourceCommitSHA string
	GitOpsCommitSHA string
	ImageRef        string
}

// CommitMessage is the machine-parseable commit contract.
type CommitMessage struct {
	Action      string
	TeamSlug    string
	AppSlug     string
	DeployRunID string
	PreviewID   string
	TraceID     string
}

// Renderer renders manifests for a GitOps change.
type Renderer interface {
	Render(ctx context.Context, input RenderInput) (RenderResult, error)
}

// Committer formats commit messages.
type Committer interface {
	CommitMessage(input CommitMessage) string
}

// Pusher commits and pushes rendered files.
type Pusher interface {
	CommitAndPush(ctx context.Context, worktreePath string, message CommitMessage, files map[string]string) (string, error)
}

// Service exposes the render/commit/push workflow.
type Service interface {
	Renderer
	Committer
	Pusher
	RenderAndPush(ctx context.Context, input RenderInput) (GitOpsResult, error)
}
