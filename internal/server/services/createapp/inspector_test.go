package createapp

import (
	"context"
	"errors"
	"testing"
)

// fakeInspectorRecord records Inspect call count and delegates to a stub result.
type fakeInspectorRecord struct {
	calls  int
	result RepoMetadata
	err    error
}

func (f *fakeInspectorRecord) Inspect(_ context.Context, _, _ string) (RepoMetadata, error) {
	f.calls++
	return f.result, f.err
}

func TestInspectorRouter_DispatchesUploadScheme(t *testing.T) {
	upload := &fakeInspectorRecord{}
	r := NewInspector(nil, nil, upload)
	_, _ = r.Inspect(context.Background(), "upload://upl_x", "")
	if upload.calls != 1 {
		t.Fatalf("upload inspector should have been called once, got %d", upload.calls)
	}
}

func TestInspectorRouter_NilUploadReturnsUnavailable(t *testing.T) {
	r := NewInspector(nil, nil, nil)
	_, err := r.Inspect(context.Background(), "upload://upl_x", "")
	if !errors.Is(err, ErrUploadInspectionUnavailable) {
		t.Fatalf("expected ErrUploadInspectionUnavailable, got %v", err)
	}
}

func TestInspectorRouter_DispatchesFileScheme(t *testing.T) {
	local := &fakeInspectorRecord{}
	r := NewInspector(nil, local, nil)
	_, _ = r.Inspect(context.Background(), "file:///tmp/repo", "")
	if local.calls != 1 {
		t.Fatalf("local inspector should have been called once, got %d", local.calls)
	}
}

func TestInspectorRouter_NilLocalReturnsDisabled(t *testing.T) {
	r := NewInspector(nil, nil, nil)
	_, err := r.Inspect(context.Background(), "file:///tmp/repo", "")
	if !errors.Is(err, ErrLocalFileRepoDisabled) {
		t.Fatalf("expected ErrLocalFileRepoDisabled, got %v", err)
	}
}

func TestInspectorRouter_DispatchesGitHubScheme(t *testing.T) {
	github := &fakeInspectorRecord{result: RepoMetadata{GitHubAppStatus: "not_applicable"}}
	r := NewInspector(github, nil, nil)
	_, _ = r.Inspect(context.Background(), "https://github.com/foo/bar", "main")
	if github.calls != 1 {
		t.Fatalf("github inspector should have been called once, got %d", github.calls)
	}
}

func TestInspectorRouter_NilGitHubReturnsEmpty(t *testing.T) {
	r := NewInspector(nil, nil, nil)
	meta, err := r.Inspect(context.Background(), "https://github.com/foo/bar", "main")
	if err != nil {
		t.Fatalf("nil github should return no error, got %v", err)
	}
	if meta.GitHubAppStatus != "not_applicable" {
		t.Errorf("github_status=%q want not_applicable", meta.GitHubAppStatus)
	}
}

func TestInspectorRouter_UploadTakesPriorityOverGitHub(t *testing.T) {
	upload := &fakeInspectorRecord{}
	github := &fakeInspectorRecord{}
	r := NewInspector(github, nil, upload)
	_, _ = r.Inspect(context.Background(), "upload://upl_test", "")
	if upload.calls != 1 {
		t.Errorf("upload inspector calls=%d want 1", upload.calls)
	}
	if github.calls != 0 {
		t.Errorf("github inspector should not be called, got %d calls", github.calls)
	}
}
