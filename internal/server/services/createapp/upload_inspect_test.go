package createapp

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
)

// fakeUploadStore satisfies uploadInspectStore using an in-memory map.
type fakeUploadStore struct {
	rows map[string]db.Upload // key = team+"/"+id
}

func (f *fakeUploadStore) GetUpload(_ context.Context, team, id string) (db.Upload, error) {
	if row, ok := f.rows[team+"/"+id]; ok {
		return row, nil
	}
	return db.Upload{}, db.ErrUploadNotFound
}

// fakeArchiveReader satisfies uploadArchiveReader using an in-memory map.
type fakeArchiveReader struct {
	files map[string]string // key = team+"/"+id+"/"+relPath
}

func (f *fakeArchiveReader) Open(_ context.Context, team, id, rel string) (io.ReadCloser, error) {
	if body, ok := f.files[team+"/"+id+"/"+rel]; ok {
		return io.NopCloser(strings.NewReader(body)), nil
	}
	return nil, errors.New("not found")
}

// ctxWithTeam injects a team ID into ctx using the test export from auth.
func ctxWithTeam(teamID string) context.Context {
	return auth.WithTeamIDForTest(context.Background(), teamID)
}

func newInspector(rows map[string]db.Upload, files map[string]string) UploadInspector {
	return UploadInspector{
		Repo:  &fakeUploadStore{rows: rows},
		Store: &fakeArchiveReader{files: files},
	}
}

func TestUploadInspector_HappyPath_PackageJSON(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_node": {ID: "upl_node", TeamID: "team-1", Status: "received"}},
		map[string]string{"team-1/upl_node/package.json": `{"name":"x"}`},
	)
	meta, err := ins.Inspect(ctx, "upload://upl_node", "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.Builder != "paketobuildpacks/builder-jammy-base" {
		t.Errorf("builder=%q", meta.Builder)
	}
	if meta.PrimaryPort != 3000 {
		t.Errorf("port=%d want 3000", meta.PrimaryPort)
	}
	if meta.GitHubAppStatus != "not_applicable" {
		t.Errorf("github_status=%q", meta.GitHubAppStatus)
	}
}

func TestUploadInspector_HappyPath_GoMod(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_go": {ID: "upl_go", TeamID: "team-1", Status: "received"}},
		map[string]string{"team-1/upl_go/go.mod": "module example.com/x\n\ngo 1.22\n"},
	)
	meta, err := ins.Inspect(ctx, "upload://upl_go", "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.PrimaryPort != 8080 {
		t.Errorf("port=%d want 8080", meta.PrimaryPort)
	}
	if meta.Builder != "paketobuildpacks/builder-jammy-base" {
		t.Errorf("builder=%q", meta.Builder)
	}
}

func TestUploadInspector_HappyPath_PyProject(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_py": {ID: "upl_py", TeamID: "team-1", Status: "received"}},
		map[string]string{"team-1/upl_py/pyproject.toml": "[tool.poetry]\nname = \"x\"\n"},
	)
	meta, err := ins.Inspect(ctx, "upload://upl_py", "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.PrimaryPort != 8000 {
		t.Errorf("port=%d want 8000", meta.PrimaryPort)
	}
}

func TestUploadInspector_HappyPath_Pinned(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_p": {ID: "upl_p", TeamID: "team-1", Status: "pinned"}},
		map[string]string{"team-1/upl_p/go.mod": "module x\n"},
	)
	_, err := ins.Inspect(ctx, "upload://upl_p", "")
	if err != nil {
		t.Fatalf("pinned upload should be inspectable: %v", err)
	}
}

func TestUploadInspector_ReadsCommitSHAFromGitHead(t *testing.T) {
	const sha = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_sha": {ID: "upl_sha", TeamID: "team-1", Status: "received"}},
		map[string]string{
			"team-1/upl_sha/package.json": `{}`,
			"team-1/upl_sha/.git/HEAD":    sha + "\n",
		},
	)
	meta, err := ins.Inspect(ctx, "upload://upl_sha", "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.CommitSHA != sha {
		t.Errorf("commit_sha=%q want %q", meta.CommitSHA, sha)
	}
}

func TestUploadInspector_ReadsBranchFromRef(t *testing.T) {
	const sha = "aabbccddaabbccddaabbccddaabbccddaabbccdd"
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_ref": {ID: "upl_ref", TeamID: "team-1", Status: "received"}},
		map[string]string{
			"team-1/upl_ref/package.json":               `{}`,
			"team-1/upl_ref/.git/HEAD":                  "ref: refs/heads/feature\n",
			"team-1/upl_ref/.git/refs/heads/feature":    sha + "\n",
		},
	)
	meta, err := ins.Inspect(ctx, "upload://upl_ref", "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.DefaultBranch != "feature" {
		t.Errorf("default_branch=%q want feature", meta.DefaultBranch)
	}
	if meta.CommitSHA != sha {
		t.Errorf("commit_sha=%q want %q", meta.CommitSHA, sha)
	}
}

func TestUploadInspector_FallsBackToMainBranch(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	// No .git/HEAD present; only package.json
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_main": {ID: "upl_main", TeamID: "team-1", Status: "received"}},
		map[string]string{"team-1/upl_main/package.json": `{}`},
	)
	meta, err := ins.Inspect(ctx, "upload://upl_main", "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.DefaultBranch != "main" {
		t.Errorf("default_branch=%q want main", meta.DefaultBranch)
	}
}

func TestUploadInspector_BuildpackDetectFailed(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	// No language marker files
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_nd": {ID: "upl_nd", TeamID: "team-1", Status: "received"}},
		map[string]string{"team-1/upl_nd/README.md": "no code"},
	)
	_, err := ins.Inspect(ctx, "upload://upl_nd", "")
	if !errors.Is(err, ErrBuildpackDetectFailed) {
		t.Fatalf("err=%v want ErrBuildpackDetectFailed", err)
	}
}

func TestUploadInspector_UploadNotFound(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{},
		map[string]string{},
	)
	_, err := ins.Inspect(ctx, "upload://upl_missing", "")
	if !errors.Is(err, ErrUploadInspectionUnavailable) {
		t.Fatalf("err=%v want ErrUploadInspectionUnavailable", err)
	}
}

func TestUploadInspector_UploadExpired(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_exp": {ID: "upl_exp", TeamID: "team-1", Status: "expired"}},
		map[string]string{},
	)
	_, err := ins.Inspect(ctx, "upload://upl_exp", "")
	if !errors.Is(err, ErrUploadInspectionUnavailable) {
		t.Fatalf("err=%v want ErrUploadInspectionUnavailable", err)
	}
}

func TestUploadInspector_UploadGCd(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_gc": {ID: "upl_gc", TeamID: "team-1", Status: "gc'd"}},
		map[string]string{},
	)
	_, err := ins.Inspect(ctx, "upload://upl_gc", "")
	if !errors.Is(err, ErrUploadInspectionUnavailable) {
		t.Fatalf("err=%v want ErrUploadInspectionUnavailable", err)
	}
}

func TestUploadInspector_URLInvalid(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(map[string]db.Upload{}, map[string]string{})
	_, err := ins.Inspect(ctx, "not-an-upload-url", "")
	if !errors.Is(err, ErrUploadURLInvalid) {
		t.Fatalf("err=%v want ErrUploadURLInvalid", err)
	}
}

func TestUploadInspector_URLInvalid_SlashInID(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(map[string]db.Upload{}, map[string]string{})
	_, err := ins.Inspect(ctx, "upload://foo/bar", "")
	if !errors.Is(err, ErrUploadURLInvalid) {
		t.Fatalf("err=%v want ErrUploadURLInvalid", err)
	}
}

func TestUploadInspector_URLInvalid_EmptyID(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := newInspector(map[string]db.Upload{}, map[string]string{})
	_, err := ins.Inspect(ctx, "upload://", "")
	if !errors.Is(err, ErrUploadURLInvalid) {
		t.Fatalf("err=%v want ErrUploadURLInvalid", err)
	}
}

func TestUploadInspector_TeamIDMissing(t *testing.T) {
	// context has no team id
	ctx := context.Background()
	ins := newInspector(
		map[string]db.Upload{"team-1/upl_x": {ID: "upl_x", TeamID: "team-1", Status: "received"}},
		map[string]string{},
	)
	_, err := ins.Inspect(ctx, "upload://upl_x", "")
	if !errors.Is(err, ErrUploadTeamMissing) {
		t.Fatalf("err=%v want ErrUploadTeamMissing", err)
	}
}

func TestUploadInspector_MissingStore(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := UploadInspector{Repo: nil, Store: nil}
	_, err := ins.Inspect(ctx, "upload://upl_x", "")
	if !errors.Is(err, ErrUploadStoreNotConfigured) {
		t.Fatalf("err=%v want ErrUploadStoreNotConfigured", err)
	}
}

func TestUploadInspector_MissingRepo(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := UploadInspector{Repo: nil, Store: &fakeArchiveReader{files: map[string]string{}}}
	_, err := ins.Inspect(ctx, "upload://upl_x", "")
	if !errors.Is(err, ErrUploadStoreNotConfigured) {
		t.Fatalf("err=%v want ErrUploadStoreNotConfigured", err)
	}
}

func TestUploadInspector_MissingArchiveReader(t *testing.T) {
	ctx := ctxWithTeam("team-1")
	ins := UploadInspector{Repo: &fakeUploadStore{rows: map[string]db.Upload{}}, Store: nil}
	_, err := ins.Inspect(ctx, "upload://upl_x", "")
	if !errors.Is(err, ErrUploadStoreNotConfigured) {
		t.Fatalf("err=%v want ErrUploadStoreNotConfigured", err)
	}
}
