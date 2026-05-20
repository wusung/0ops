package createapp

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
)

// UploadInspector inspects an upload://<upload_id> repo by reading the
// server-side ingest tree (T6) under <APP_SOURCE_INGEST_ROOT>/<team>/<upload>/tree.
//
// Team scope: the inspector trusts auth.TeamID(ctx) — it does NOT extract
// team from the URL. This matches how the upload archive handler (T9)
// authoritatively scopes by claims.TeamID; T11 will route the request
// from createapp.Service which already operates inside a team context.
//
// Status gate: only uploads in 'received' or 'pinned' status are inspectable.
// Expired / GC'd uploads return ErrUploadInspectionUnavailable.
type UploadInspector struct {
	Repo  uploadInspectStore
	Store uploadArchiveReader
}

// uploadInspectStore is the DB read boundary used by the inspector.
// Production: *db.Repository (T5). Tests substitute an in-memory fake.
type uploadInspectStore interface {
	GetUpload(ctx context.Context, teamID, id string) (db.Upload, error)
}

// uploadArchiveReader is the ingest-tree read boundary used by the inspector.
// Production: *ingestion.Store (T6). Tests substitute an in-memory fake.
// Only the Open method is needed — Archive is for the HTTP download path (T9).
type uploadArchiveReader interface {
	Open(ctx context.Context, teamID, uploadID, relPath string) (io.ReadCloser, error)
}

// Sentinels. ErrUploadInspectionUnavailable covers both "not found" and
// "expired/gc'd" so the inspector layer doesn't leak which one a caller hit
// (cross-team isolation property inherited from T5's ErrUploadNotFound).
var (
	ErrUploadInspectionUnavailable = errors.New("createapp: upload not available for inspection")
	ErrUploadStoreNotConfigured    = errors.New("createapp: upload inspector not configured")
	ErrUploadURLInvalid            = errors.New("createapp: upload URL invalid")
	ErrUploadTeamMissing           = errors.New("createapp: team id missing from context")
)

// Inspect parses the "upload://<upload_id>" URL, resolves the upload row,
// and reads minimal metadata from the ingest tree (.git/HEAD for commit sha,
// language-marker files for the paketo builder hint).
//
// The ref parameter is accepted for interface parity with Inspector but
// not used; the upload's content is immutable, so any ref is implicitly
// "the bytes that were uploaded."
func (u UploadInspector) Inspect(ctx context.Context, repoURL, _ string) (RepoMetadata, error) {
	if u.Repo == nil || u.Store == nil {
		return RepoMetadata{}, ErrUploadStoreNotConfigured
	}
	uploadID, err := parseUploadURL(repoURL)
	if err != nil {
		return RepoMetadata{}, err
	}
	teamID := auth.TeamID(ctx)
	if teamID == "" {
		return RepoMetadata{}, ErrUploadTeamMissing
	}
	row, err := u.Repo.GetUpload(ctx, teamID, uploadID)
	if err != nil {
		if errors.Is(err, db.ErrUploadNotFound) {
			return RepoMetadata{}, ErrUploadInspectionUnavailable
		}
		return RepoMetadata{}, err
	}
	// Status gate — only received/pinned can be inspected. Expired/gc'd are
	// treated as not-found from the inspector's perspective so callers can
	// map the failure uniformly.
	switch row.Status {
	case "received", "pinned":
		// proceed
	default:
		return RepoMetadata{}, ErrUploadInspectionUnavailable
	}

	meta := RepoMetadata{
		GitHubAppStatus: "not_applicable",
	}

	// Try to read .git/HEAD for commit sha. T6's Store preserves uploaded .git
	// metadata. Failures here are non-fatal; commit sha may legitimately be
	// empty (some clients upload a non-git source tree).
	if head, err := readUploadFile(ctx, u.Store, teamID, uploadID, ".git/HEAD"); err == nil {
		meta.CommitSHA, meta.DefaultBranch = parseGitHEAD(ctx, u.Store, teamID, uploadID, head)
	}
	if meta.DefaultBranch == "" {
		meta.DefaultBranch = "main"
	}

	builder, port, ok := detectPaketoUpload(ctx, u.Store, teamID, uploadID)
	if !ok {
		return RepoMetadata{}, ErrBuildpackDetectFailed
	}
	meta.Builder = builder
	meta.PrimaryPort = port
	return meta, nil
}

func parseUploadURL(repoURL string) (string, error) {
	if !strings.HasPrefix(repoURL, "upload://") {
		return "", ErrUploadURLInvalid
	}
	id := strings.TrimPrefix(repoURL, "upload://")
	if id == "" || strings.ContainsAny(id, "/\\") {
		return "", ErrUploadURLInvalid
	}
	return id, nil
}

// readUploadFile reads a single file's content from the ingest tree (capped
// at 64 KiB to defend against arbitrarily-large config files).
func readUploadFile(ctx context.Context, store uploadArchiveReader, teamID, uploadID, relPath string) (string, error) {
	rc, err := store.Open(ctx, teamID, uploadID, relPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	const maxRead = 64 << 10
	buf, err := io.ReadAll(io.LimitReader(rc, maxRead))
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// parseGitHEAD interprets the content of .git/HEAD. If HEAD points to a
// ref (e.g., "ref: refs/heads/main"), follow the ref to read the SHA from
// .git/refs/heads/<branch>. If HEAD already contains a SHA (detached HEAD),
// return it directly. Branch name is returned when discoverable.
func parseGitHEAD(ctx context.Context, store uploadArchiveReader, teamID, uploadID, head string) (sha, branch string) {
	head = strings.TrimSpace(head)
	if strings.HasPrefix(head, "ref: ") {
		ref := strings.TrimPrefix(head, "ref: ")
		ref = strings.TrimSpace(ref)
		// ref is typically "refs/heads/main"
		const prefix = "refs/heads/"
		if strings.HasPrefix(ref, prefix) {
			branch = strings.TrimPrefix(ref, prefix)
		}
		// Read .git/<ref> to get the actual sha.
		if shaFile, err := readUploadFile(ctx, store, teamID, uploadID, ".git/"+ref); err == nil {
			sha = strings.TrimSpace(shaFile)
		}
		return sha, branch
	}
	// Detached HEAD: head is the sha directly.
	return head, ""
}

// detectPaketoUpload is the upload-tree analogue of detectPaketo in
// local_inspect.go. It probes for known language markers via Store.Open;
// the existence test is "Open returns nil error."
func detectPaketoUpload(ctx context.Context, store uploadArchiveReader, teamID, uploadID string) (string, int, bool) {
	type marker struct {
		path    string
		builder string
		port    int
	}
	markers := []marker{
		{"package.json", "paketobuildpacks/builder-jammy-base", 3000},
		{"go.mod", "paketobuildpacks/builder-jammy-base", 8080},
		{"pyproject.toml", "paketobuildpacks/builder-jammy-base", 8000},
		{"requirements.txt", "paketobuildpacks/builder-jammy-base", 8000},
	}
	for _, m := range markers {
		rc, err := store.Open(ctx, teamID, uploadID, m.path)
		if err == nil {
			_ = rc.Close()
			return m.builder, m.port, true
		}
	}
	return "", 0, false
}
