package createapp

import (
	"context"
	"errors"
	"testing"

	"github.com/winshare/zeroops/internal/shared/dto"
)

func TestSourceToInspectURL_GitHub(t *testing.T) {
	src := dto.Source{
		Type:   dto.SourceKindGitHub,
		GitHub: &dto.SourceGitHub{URL: "https://github.com/foo/bar", Ref: "main"},
	}
	got, err := SourceToInspectURL(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://github.com/foo/bar" {
		t.Errorf("url=%q want https://github.com/foo/bar", got)
	}
}

func TestSourceToInspectURL_Upload(t *testing.T) {
	src := dto.Source{
		Type:   dto.SourceKindUpload,
		Upload: &dto.SourceUpload{UploadID: "upl_abc123"},
	}
	got, err := SourceToInspectURL(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "upload://upl_abc123" {
		t.Errorf("url=%q want upload://upl_abc123", got)
	}
}

func TestSourceToInspectURL_UnknownKind(t *testing.T) {
	src := dto.Source{Type: "s3"}
	_, err := SourceToInspectURL(src)
	if !errors.Is(err, ErrSourceUnsupported) {
		t.Fatalf("err=%v want ErrSourceUnsupported", err)
	}
}

func TestSourceToInspectURL_GitHubMissingPayload(t *testing.T) {
	src := dto.Source{Type: dto.SourceKindGitHub}
	_, err := SourceToInspectURL(src)
	if !errors.Is(err, ErrSourceUnsupported) {
		t.Fatalf("err=%v want ErrSourceUnsupported", err)
	}
}

func TestSourceToInspectURL_UploadMissingPayload(t *testing.T) {
	src := dto.Source{Type: dto.SourceKindUpload}
	_, err := SourceToInspectURL(src)
	if !errors.Is(err, ErrSourceUnsupported) {
		t.Fatalf("err=%v want ErrSourceUnsupported", err)
	}
}

func TestSourceRef_GitHub(t *testing.T) {
	src := dto.Source{
		Type:   dto.SourceKindGitHub,
		GitHub: &dto.SourceGitHub{URL: "https://github.com/foo/bar", Ref: "v1.2.3"},
	}
	if got := SourceRef(src); got != "v1.2.3" {
		t.Errorf("ref=%q want v1.2.3", got)
	}
}

func TestSourceRef_Upload(t *testing.T) {
	src := dto.Source{
		Type:   dto.SourceKindUpload,
		Upload: &dto.SourceUpload{UploadID: "upl_x", Ref: "feature-branch"},
	}
	if got := SourceRef(src); got != "feature-branch" {
		t.Errorf("ref=%q want feature-branch", got)
	}
}

func TestSourceRef_UploadNoRef(t *testing.T) {
	src := dto.Source{
		Type:   dto.SourceKindUpload,
		Upload: &dto.SourceUpload{UploadID: "upl_x"},
	}
	if got := SourceRef(src); got != "" {
		t.Errorf("ref=%q want empty", got)
	}
}

func TestSourceRef_Unknown(t *testing.T) {
	src := dto.Source{Type: "s3"}
	if got := SourceRef(src); got != "" {
		t.Errorf("ref=%q want empty", got)
	}
}

// stubInspector records call arguments for SourceFactory delegation tests.
type stubInspector struct {
	gotURL string
	gotRef string
	result RepoMetadata
	err    error
}

func (s *stubInspector) Inspect(_ context.Context, repoURL, ref string) (RepoMetadata, error) {
	s.gotURL = repoURL
	s.gotRef = ref
	return s.result, s.err
}

func TestSourceFactory_Inspect_GitHub(t *testing.T) {
	stub := &stubInspector{result: RepoMetadata{Builder: "paketo", GitHubAppStatus: "not_applicable"}}
	sf := SourceFactory{Inspector: stub}
	src := dto.Source{
		Type:   dto.SourceKindGitHub,
		GitHub: &dto.SourceGitHub{URL: "https://github.com/foo/bar", Ref: "main"},
	}
	meta, err := sf.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Builder != "paketo" {
		t.Errorf("builder=%q want paketo", meta.Builder)
	}
	if stub.gotURL != "https://github.com/foo/bar" {
		t.Errorf("got url=%q want https://github.com/foo/bar", stub.gotURL)
	}
	if stub.gotRef != "main" {
		t.Errorf("got ref=%q want main", stub.gotRef)
	}
}

func TestSourceFactory_Inspect_Upload(t *testing.T) {
	stub := &stubInspector{result: RepoMetadata{Builder: "paketo", GitHubAppStatus: "not_applicable"}}
	sf := SourceFactory{Inspector: stub}
	src := dto.Source{
		Type:   dto.SourceKindUpload,
		Upload: &dto.SourceUpload{UploadID: "upl_abc", Ref: "v2"},
	}
	_, err := sf.Inspect(context.Background(), src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.gotURL != "upload://upl_abc" {
		t.Errorf("got url=%q want upload://upl_abc", stub.gotURL)
	}
	if stub.gotRef != "v2" {
		t.Errorf("got ref=%q want v2", stub.gotRef)
	}
}

func TestSourceFactory_Inspect_NilInspector(t *testing.T) {
	sf := SourceFactory{Inspector: nil}
	src := dto.Source{
		Type:   dto.SourceKindGitHub,
		GitHub: &dto.SourceGitHub{URL: "https://github.com/foo/bar", Ref: "main"},
	}
	_, err := sf.Inspect(context.Background(), src)
	if err == nil {
		t.Fatal("expected error for nil Inspector")
	}
}

func TestSourceFactory_Inspect_UnsupportedKind(t *testing.T) {
	stub := &stubInspector{}
	sf := SourceFactory{Inspector: stub}
	src := dto.Source{Type: "s3"}
	_, err := sf.Inspect(context.Background(), src)
	if !errors.Is(err, ErrSourceUnsupported) {
		t.Fatalf("err=%v want ErrSourceUnsupported", err)
	}
}
