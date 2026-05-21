package server

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/klauspost/compress/zstd"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/middleware/ratelimit"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// --- fakeIngest: in-memory substitute for ingestion.Store ---

type fakeIngest struct {
	// corruptPrefixes causes Put to return ErrPathEscape when the stream
	// begins with the sentinel bytes.
	corruptPrefix []byte
	// oversizeAfter causes Put to return ErrArchiveTooLarge after this many bytes.
	// Zero means no limit.
	oversizeAfter int64
	// returnErr is returned verbatim when non-nil (overrides other failure modes).
	returnErr error

	// lastStored captures the most recent Stored result for inspection.
	lastStored ingestion.Stored
}

func (f *fakeIngest) Put(_ context.Context, teamID, uploadID string, r io.Reader, format string) (ingestion.Stored, error) {
	if f.returnErr != nil {
		return ingestion.Stored{}, f.returnErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return ingestion.Stored{}, err
	}
	if len(f.corruptPrefix) > 0 && bytes.HasPrefix(body, f.corruptPrefix) {
		return ingestion.Stored{}, fmt.Errorf("tar entry %q: %w", "evil/../escape", ingestion.ErrPathEscape)
	}
	if f.oversizeAfter > 0 && int64(len(body)) > f.oversizeAfter {
		return ingestion.Stored{}, ingestion.ErrArchiveTooLarge
	}
	stored := ingestion.Stored{
		SHA256:     fakeSHA256(body),
		SizeBytes:  int64(len(body)),
		EntryCount: 1,
		Format:     format,
		ReceivedAt: time.Now().UTC(),
		Path:       "/fake/" + teamID + "/" + uploadID,
	}
	f.lastStored = stored
	return stored, nil
}

// fakeSHA256 returns a deterministic hex string derived from the body
// length + first bytes, so tests can predict or check it easily.
func fakeSHA256(body []byte) string {
	// Use a simple but deterministic value for test purposes.
	// Real sha256 computed by production Store; this fake just needs a
	// non-empty hex string for the response shape test.
	h := uint64(len(body))
	for i := 0; i < len(body) && i < 8; i++ {
		h = h*31 + uint64(body[i])
	}
	return fmt.Sprintf("%064x", h)
}

// --- fakeUploadAudit: slice-append fake for audit assertions ---

type fakeUploadAudit struct {
	entries []audit.Entry
}

func (f *fakeUploadAudit) Log(_ context.Context, entry audit.Entry) error {
	f.entries = append(f.entries, entry)
	return nil
}

// --- helpers for building multipart bodies ---

// buildMultipart creates a multipart/form-data body with optional fields.
// archiveData is written under field "archive"; sha256Hex under "sha256"
// (pass "" to omit the sha256 field).
func buildMultipart(t *testing.T, archiveData []byte, sha256Hex string) (body *bytes.Buffer, contentType string) {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)

	if sha256Hex != "" {
		w, err := mw.CreateFormField("sha256")
		if err != nil {
			t.Fatalf("create sha256 field: %v", err)
		}
		if _, err := io.WriteString(w, sha256Hex); err != nil {
			t.Fatalf("write sha256: %v", err)
		}
	}

	if archiveData != nil {
		w, err := mw.CreateFormFile("archive", "app.tar.zst")
		if err != nil {
			t.Fatalf("create archive field: %v", err)
		}
		if _, err := w.Write(archiveData); err != nil {
			t.Fatalf("write archive: %v", err)
		}
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf, mw.FormDataContentType()
}

// makeTarZst builds a minimal tar.zst in memory with a single file entry.
func makeTarZst(t *testing.T, filename, content string) []byte {
	t.Helper()
	// First build the tar.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	body := []byte(content)
	hdr := &tar.Header{
		Name:     filename,
		Mode:     0644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar write header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}

	// Now zstd-compress.
	var zstBuf bytes.Buffer
	enc, err := zstd.NewWriter(&zstBuf)
	if err != nil {
		t.Fatalf("zstd new writer: %v", err)
	}
	if _, err := enc.Write(tarBuf.Bytes()); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return zstBuf.Bytes()
}

// fakeIngestFull wraps fakeIngest and adds a stub Archive method so it
// satisfies ingestionStoreFull (needed for newRouterFull calls in tests).
type fakeIngestFull struct {
	*fakeIngest
	archiveContents map[string][]byte // key: teamID+"/"+uploadID
	archiveErr      error
}

func newFakeIngestFull(fi *fakeIngest) *fakeIngestFull {
	return &fakeIngestFull{fakeIngest: fi, archiveContents: map[string][]byte{}}
}

func (f *fakeIngestFull) Archive(_ context.Context, teamID, uploadID string) (io.ReadCloser, error) {
	if f.archiveErr != nil {
		return nil, f.archiveErr
	}
	key := teamID + "/" + uploadID
	data, ok := f.archiveContents[key]
	if !ok {
		return nil, ingestion.ErrUploadNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Open satisfies ingestionReadOpener (widened in T12). Returns ErrUploadNotFound
// for all paths — fakeIngestFull tests don't exercise the Open path.
func (f *fakeIngestFull) Open(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	return nil, ingestion.ErrUploadNotFound
}

// fakeArchiveReader is a standalone archiveReader fake used for archive tests.
type fakeArchiveReader struct {
	contents map[string][]byte // key: teamID+"/"+uploadID
	err      error
}

func (f *fakeArchiveReader) Archive(_ context.Context, teamID, uploadID string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	key := teamID + "/" + uploadID
	data, ok := f.contents[key]
	if !ok {
		return nil, ingestion.ErrUploadNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Open satisfies ingestionReadOpener (widened in T12). Returns ErrUploadNotFound
// for all paths — archive reader tests don't exercise the Open path.
func (f *fakeArchiveReader) Open(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	return nil, ingestion.ErrUploadNotFound
}

// newUploadRouter wires a Router+fakeStore pair for upload handler tests.
// The returned triple (server, token, store) provides everything a test needs.
func newUploadRouter(t *testing.T, ingest ingestionStore, auditSvc uploadAuditWriter) (*httptest.Server, string, *fakeStore) {
	t.Helper()
	store, token := newFakeStore()
	// Wrap ingest in fakeIngestFull so it satisfies ingestionStoreFull.
	fi, ok := ingest.(*fakeIngest)
	if !ok {
		t.Fatal("newUploadRouter: ingest must be *fakeIngest")
	}
	full := newFakeIngestFull(fi)
	// Inline a local newRouterFull call: we reuse NewRouter but also need to
	// inject uploadIngest and uploadAuditSvc. Use newRouterFull directly since
	// it is unexported but we're in the same package.
	h := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, full, auditSvc, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, token, store
}

// newArchiveTestEnv builds a minimal server for archive download tests.
// It returns the store (for seeding uploads), the token signer, the archive
// fake (for seeding archive bytes), the audit fake, and the test server.
func newArchiveTestEnv(t *testing.T) (store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, fa *fakeUploadAudit, srv *httptest.Server) {
	t.Helper()
	store, _ = newFakeStore()
	signer = &ingestion.TokenSigner{Secret: []byte("test-archive-secret"), TTL: time.Hour}
	arc = &fakeArchiveReader{contents: map[string][]byte{}}
	fa = &fakeUploadAudit{}

	// Build a combined ingestionStoreFull from fakeIngest (for Put) + fakeArchiveReader.
	type combinedIngest struct {
		*fakeIngest
		*fakeArchiveReader
	}
	combined := &combinedIngest{
		fakeIngest:       &fakeIngest{},
		fakeArchiveReader: arc,
	}
	h := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, combined, fa, signer)
	srv = httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return
}

// --- tests ---

func TestUploadsPostHappyPath(t *testing.T) {
	archive := makeTarZst(t, "hello.txt", "hello world")
	fi := &fakeIngest{}
	fa := &fakeUploadAudit{}
	srv, token, store := newUploadRouter(t, fi, fa)

	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body: %s", resp.StatusCode, raw)
	}

	var out dto.UploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !strings.HasPrefix(out.UploadID, "upl_") {
		t.Errorf("upload_id %q should start with upl_", out.UploadID)
	}
	if out.TeamID != "team-1" {
		t.Errorf("team_id = %q, want team-1", out.TeamID)
	}
	if out.SizeBytes <= 0 {
		t.Errorf("size_bytes = %d, should be positive", out.SizeBytes)
	}
	if len(out.SHA256) != 64 {
		t.Errorf("sha256 should be 64 hex chars, got len=%d", len(out.SHA256))
	}
	if out.Format != "tar.zst" {
		t.Errorf("format = %q, want tar.zst", out.Format)
	}
	wantExpiry := time.Now().Add(uploadInertTTL)
	diff := out.ExpiresAt.Sub(wantExpiry)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("expires_at = %v, want ~%v (±5s)", out.ExpiresAt, wantExpiry)
	}
	if out.ReceivedAt.IsZero() {
		t.Error("received_at should not be zero")
	}

	// Confirm upload row was inserted.
	if len(store.uploadRows) != 1 {
		t.Fatalf("uploadRows len = %d, want 1", len(store.uploadRows))
	}
	if store.uploadRows[0].Status != "received" {
		t.Errorf("upload status = %q, want received", store.uploadRows[0].Status)
	}
}

func TestUploadsPostRejectsOversize(t *testing.T) {
	archive := makeTarZst(t, "big.txt", "data")
	fi := &fakeIngest{oversizeAfter: 1} // any positive content triggers oversize
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "payload_too_large" {
		t.Errorf("error code = %q, want payload_too_large", code)
	}
}

func TestUploadsPostRejectsBadArchive(t *testing.T) {
	// Non-archive bytes (no valid magic).
	notAnArchive := []byte("hello this is not an archive at all")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, notAnArchive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "unsupported_archive_format" {
		t.Errorf("error code = %q, want unsupported_archive_format", code)
	}
}

func TestUploadsPostRejectsCorruptTar(t *testing.T) {
	// Valid zstd header bytes followed by corrupt tar content.
	// The fakeIngest is configured to return ErrPathEscape for a sentinel
	// prefix that we embed after the magic bytes.
	corruptSentinel := []byte("\x28\xb5\x2f\xfd__path_escape__")
	fi := &fakeIngest{corruptPrefix: corruptSentinel}
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, corruptSentinel, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "archive_corrupt" {
		t.Errorf("error code = %q, want archive_corrupt", code)
	}
}

func TestUploadsPostRequiresAuth(t *testing.T) {
	fi := &fakeIngest{}
	srv, _, store := newUploadRouter(t, fi, nil)

	archive := makeTarZst(t, "f.txt", "x")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Content-Type", ct)
	// No Authorization header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestUploadsPostRequiresMembership(t *testing.T) {
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	// Use a team slug that doesn't exist in the store.
	archive := makeTarZst(t, "f.txt", "x")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/nonexistent-team/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)
	_ = store // suppress unused warning

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	// mw.ResolveTeam returns 404 when team_slug resolves to nothing.
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUploadsPostRequiresScope(t *testing.T) {
	// Build a token that has apps:read but NOT apps:write.
	store, _ := newFakeStore()
	// Create a restricted token without apps:write.
	readOnlyToken, err := store.CreateCLIToken(context.Background(), "user-1", "team-1", []string{"apps:read", "teams:read"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	fi := newFakeIngestFull(&fakeIngest{})
	h := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, fi, nil, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	archive := makeTarZst(t, "f.txt", "x")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestUploadsPostSHA256Mismatch(t *testing.T) {
	archive := makeTarZst(t, "f.txt", "hello")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	// Supply a deliberately wrong sha256.
	body, ct := buildMultipart(t, archive, strings.Repeat("a", 64))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "sha256_mismatch" {
		t.Errorf("error code = %q, want sha256_mismatch", code)
	}
}

func TestUploadsPostMissingArchivePart(t *testing.T) {
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	// Multipart body with only the sha256 field, no archive.
	body, ct := buildMultipart(t, nil, strings.Repeat("b", 64))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "validation_failed" {
		t.Errorf("error code = %q, want validation_failed", code)
	}
}

func TestUploadsPostAuditWritten(t *testing.T) {
	archive := makeTarZst(t, "main.go", "package main")
	fi := &fakeIngest{}
	fa := &fakeUploadAudit{}
	srv, token, store := newUploadRouter(t, fi, fa)

	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; body: %s", resp.StatusCode, raw)
	}

	// Parse the response to get the upload_id that was assigned.
	var out dto.UploadResponse
	// Note: body was already consumed; re-read via resp is fine since we
	// haven't read it yet — but we consumed it above. Decode from a second
	// request is not possible. Instead capture out from the body before this
	// point. The architecture here: we read body in the status check only if
	// non-201; if 201 we don't consume body in that branch, so Decode is fine.
	_ = out // parsed below via a re-read — actually body is not consumed
	// The body above is only read on error branch (t.Fatalf), so on success
	// the decoder below is fine.
	// Re-read using the resp.Body — already deferred close, still open here.

	// Actually we need to re-decode from the response. The body is still open
	// at this point since the error branch uses t.Fatalf (which calls t.Fatal
	// which marks the test failed but does NOT stop execution in this closure
	// — actually t.Fatalf does call runtime.Goexit which ends the goroutine).
	// So on the success path we haven't consumed the body yet.
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(fa.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(fa.entries))
	}
	entry := fa.entries[0]
	if entry.Action != "app_source.upload.created" {
		t.Errorf("audit action = %q, want app_source.upload.created", entry.Action)
	}
	if entry.SubjectID == nil || *entry.SubjectID != out.UploadID {
		t.Errorf("audit SubjectID = %v, want %q", entry.SubjectID, out.UploadID)
	}
	if entry.Outcome != audit.OutcomeSuccess {
		t.Errorf("audit outcome = %q, want success", entry.Outcome)
	}
}

func TestUploadsPostUnreachableWithoutIngestStore(t *testing.T) {
	// NewRouter passes nil for uploadIngest; the route must not be registered.
	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	// Use raw zstd magic bytes to avoid any format-detection rejection.
	body, ct := buildMultipart(t, []byte{0x28, 0xb5, 0x2f, 0xfd, 0, 0, 0, 0}, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 404/405 (route not registered), got %d", resp.StatusCode)
	}
}

func TestUploadsPostSHA256MatchPassthrough(t *testing.T) {
	// If sha256 field matches the server-computed value, request should succeed.
	archive := makeTarZst(t, "x.txt", "data")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	// First, do a dry-run POST without sha256 to discover the hash.
	body1, ct1 := buildMultipart(t, archive, "")
	req1, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body1)
	req1.Header.Set("Authorization", "Bearer "+token)
	req1.Header.Set("Content-Type", ct1)
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	var out1 dto.UploadResponse
	_ = json.NewDecoder(resp1.Body).Decode(&out1)
	resp1.Body.Close()

	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first request status = %d, want 201", resp1.StatusCode)
	}

	// Re-POST same archive with the correct sha256 — should still be 201.
	body2, ct2 := buildMultipart(t, archive, out1.SHA256)
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body2)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", ct2)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("second request status = %d; body: %s", resp2.StatusCode, raw)
	}
}

func TestUploadsPostRejectsTooManyParts(t *testing.T) {
	// Multipart body with more than maxMultipartParts parts.
	// The handler must reject with 400 validation_failed before finishing the loop.
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Write maxMultipartParts+1 noise fields — exceeds the cap.
	for i := 0; i < maxMultipartParts+1; i++ {
		f, err := mw.CreateFormField(fmt.Sprintf("noise%d", i))
		if err != nil {
			t.Fatalf("create noise field: %v", err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatalf("write noise field: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, raw)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "validation_failed" {
		t.Errorf("error code = %q, want validation_failed", code)
	}
	msg, _ := errBody["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "too many multipart parts") {
		t.Errorf("error message = %q, want to contain 'too many multipart parts'", msg)
	}
}

func TestUploadsPostRejectsDuplicateArchive(t *testing.T) {
	// Multipart body with two "archive" parts — handler must reject the second.
	archive := makeTarZst(t, "dup.txt", "duplicate")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Write archive part once.
	pw1, err := mw.CreateFormFile("archive", "app.tar.zst")
	if err != nil {
		t.Fatalf("create first archive part: %v", err)
	}
	if _, err := pw1.Write(archive); err != nil {
		t.Fatalf("write first archive: %v", err)
	}

	// Write archive part a second time.
	pw2, err := mw.CreateFormFile("archive", "app2.tar.zst")
	if err != nil {
		t.Fatalf("create second archive part: %v", err)
	}
	if _, err := pw2.Write(archive); err != nil {
		t.Fatalf("write second archive: %v", err)
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, raw)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "validation_failed" {
		t.Errorf("error code = %q, want validation_failed", code)
	}
	msg, _ := errBody["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "duplicate archive part") {
		t.Errorf("error message = %q, want to contain 'duplicate archive part'", msg)
	}
}

// ─── Archive download tests (T9) ───────────────────────────────────────────

// archiveGetURL builds the GET URL for a given upload ID.
func archiveGetURL(srv *httptest.Server, id string) string {
	return srv.URL + "/v1/uploads/" + id + "/archive"
}

// mintArchiveToken is a shorthand for signing a valid archive token in tests.
func mintArchiveToken(t *testing.T, signer *ingestion.TokenSigner, claims ingestion.TokenClaims) string {
	t.Helper()
	tok, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

// seedArchiveUpload inserts an upload row in the fake store and plants
// archive bytes keyed by teamID/uploadID in the fake archive reader.
func seedArchiveUpload(store *fakeStore, arc *fakeArchiveReader, u db.Upload, data []byte) {
	store.seedUpload(u)
	arc.contents[u.TeamID+"/"+u.ID] = data
}

func TestUploadsArchiveGetHappyPath(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("fake-archive-bytes-zstd")
	u := db.Upload{
		ID: "upl_happy", TeamID: "team-1",
		SizeBytes: int64(len(archiveData)), ArchiveFormat: "tar.zst", Status: "received",
	}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_happy"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_happy"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zstd" {
		t.Errorf("Content-Type = %q, want application/zstd", ct)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, archiveData) {
		t.Errorf("body mismatch: got %d bytes, want %d", len(got), len(archiveData))
	}
}

func TestUploadsArchiveGetMissingToken(t *testing.T) {
	_, _, _, _, srv := newArchiveTestEnv(t)

	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_x"), nil)
	// No Authorization header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestUploadsArchiveGetMalformedToken(t *testing.T) {
	_, _, _, _, srv := newArchiveTestEnv(t)

	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_x"), nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestUploadsArchiveGetEmptyBearerToken(t *testing.T) {
	_, _, _, _, srv := newArchiveTestEnv(t)

	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_x"), nil)
	req.Header.Set("Authorization", "Bearer ") // prefix present, token empty

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for empty bearer token, got %d", resp.StatusCode)
	}
}

func TestUploadsArchiveGetExpiredToken(t *testing.T) {
	_, _, _, _, srv := newArchiveTestEnv(t)

	// Sign with a signer whose TTL places expiry in the past.
	expiredSigner := &ingestion.TokenSigner{Secret: []byte("test-archive-secret"), TTL: -time.Minute}
	tok := mintArchiveToken(t, expiredSigner, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_x"})

	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_x"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 401; body: %s", resp.StatusCode, body)
	}
}

func TestUploadsArchiveGetWrongScope(t *testing.T) {
	_, _, _, _, srv := newArchiveTestEnv(t)

	// Forge a token with wrong scope by signing directly with the jwt library,
	// bypassing TokenSigner.Sign which always forces ScopeDownloadUpload.
	mapClaims := jwt.MapClaims{
		"team_id":   "team-1",
		"upload_id": "upl_x",
		"scope":     "wrong-scope",
		"iss":       "0ops",
		"aud":       jwt.ClaimStrings{"gha-build"},
		"sub":       "upload:upl_x",
		"iat":       time.Now().Add(-30 * time.Second).Unix(),
		"exp":       time.Now().Add(time.Hour).Unix(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, mapClaims).SignedString([]byte("test-archive-secret"))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_x"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403; body: %s", resp.StatusCode, body)
	}
}

func TestUploadsArchiveGetURLUploadIDMismatch(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("data")
	u := db.Upload{ID: "upl_A", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "received"}
	seedArchiveUpload(store, arc, u, archiveData)

	// Token says upload_id=upl_A but URL says upl_B.
	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_A"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_B"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403; body: %s", resp.StatusCode, body)
	}
}

func TestUploadsArchiveGetCrossTeamNotFound(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	// Upload belongs to team-2, but token claims team-1.
	archiveData := []byte("data")
	u := db.Upload{ID: "upl_cross", TeamID: "team-2", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "received"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_cross"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_cross"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "source_not_found" {
		t.Errorf("error code = %q, want source_not_found", code)
	}
}

func TestUploadsArchiveGetExpiredUpload(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("data")
	u := db.Upload{ID: "upl_exp", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "expired"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_exp"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_exp"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "source_expired" {
		t.Errorf("error code = %q, want source_expired", code)
	}
}

func TestUploadsArchiveGetGCdUpload(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("data")
	u := db.Upload{ID: "upl_gcd", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "gc'd"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_gcd"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_gcd"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "source_expired" {
		t.Errorf("error code = %q, want source_expired", code)
	}
}

func TestUploadsArchiveGetReceived(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("received-data")
	u := db.Upload{ID: "upl_recv", TeamID: "team-1", SizeBytes: int64(len(archiveData)), ArchiveFormat: "tar.zst", Status: "received"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_recv"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_recv"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

func TestUploadsArchiveGetPinned(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("pinned-data")
	u := db.Upload{ID: "upl_pin", TeamID: "team-1", SizeBytes: int64(len(archiveData)), ArchiveFormat: "tar.zst", Status: "pinned"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_pin"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_pin"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
}

func TestUploadsArchiveGetNotFound(t *testing.T) {
	_, signer, _, _, srv := newArchiveTestEnv(t)

	// No upload seeded for this ID.
	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_nope"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_nope"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, body)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != "source_not_found" {
		t.Errorf("error code = %q, want source_not_found", code)
	}
}

func TestUploadsArchiveGetTarGzContentType(t *testing.T) {
	store, signer, arc, _, srv := newArchiveTestEnv(t)

	archiveData := []byte("gzip-archive-data")
	u := db.Upload{ID: "upl_gz", TeamID: "team-1", SizeBytes: int64(len(archiveData)), ArchiveFormat: "tar.gz", Status: "received"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_gz"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_gz"), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
}

func TestUploadsArchiveGetAuditWritten(t *testing.T) {
	store, signer, arc, fa, srv := newArchiveTestEnv(t)

	archiveData := []byte("audit-check-data")
	uploadID := "upl_audit"
	u := db.Upload{ID: uploadID, TeamID: "team-1", SizeBytes: int64(len(archiveData)), ArchiveFormat: "tar.zst", Status: "received"}
	seedArchiveUpload(store, arc, u, archiveData)

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: uploadID, DeployRunID: "run-42"})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, uploadID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// Drain body so the handler completes before we check audit.
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if len(fa.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(fa.entries))
	}
	entry := fa.entries[0]
	if entry.Action != "app_source.upload.archive_downloaded" {
		t.Errorf("audit action = %q, want app_source.upload.archive_downloaded", entry.Action)
	}
	if entry.SubjectID == nil || *entry.SubjectID != uploadID {
		t.Errorf("audit SubjectID = %v, want %q", entry.SubjectID, uploadID)
	}
	if entry.Outcome != audit.OutcomeSuccess {
		t.Errorf("audit outcome = %q, want success", entry.Outcome)
	}
	if entry.ActorUserID != nil {
		t.Errorf("ActorUserID should be nil for workflow actor, got %v", entry.ActorUserID)
	}
	if entry.Source != audit.SourceSystem {
		t.Errorf("audit source = %q, want system", entry.Source)
	}
	result, _ := entry.Result.(map[string]any)
	if result["deploy_run_id"] != "run-42" {
		t.Errorf("audit result deploy_run_id = %v, want run-42", result["deploy_run_id"])
	}
}

func TestUploadsArchiveGetUnreachableWithoutSigner(t *testing.T) {
	// NewRouter passes nil for archiveSigner; the route must not be registered.
	store, _ := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/uploads/upl_any/archive", nil)
	req.Header.Set("Authorization", "Bearer dummy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 404/405 (route not registered), got %d", resp.StatusCode)
	}
}

// ─── T20 quota handler tests ──────────────────────────────────────────────

// newUploadRouterWithQuotaStore builds a test server with a manually-supplied
// fakeStore so tests can pre-seed uploadRows for quota checks.
func newUploadRouterWithQuotaStore(t *testing.T, store *fakeStore, ingest ingestionStore, auditSvc uploadAuditWriter) *httptest.Server {
	t.Helper()
	fi, ok := ingest.(*fakeIngest)
	if !ok {
		t.Fatal("newUploadRouterWithQuotaStore: ingest must be *fakeIngest")
	}
	full := newFakeIngestFull(fi)
	h := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, full, auditSvc, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// starterQuotas returns DefaultUploadQuotas for the PlanStarter tier.
// The fakeStore team is on plan "starter", so handler uses Starter caps.
func starterQuotas() UploadQuotaTier { return DefaultUploadQuotas()[ratelimit.PlanStarter] }

func TestUploadsPost_QuotaInertBytes(t *testing.T) {
	// Pre-seed the store such that inert bytes + DefaultUploadMaxArchiveBytes > Starter cap.
	// fakeStore team plan is "starter" — handler will use PlanStarter quotas.
	store, token := newFakeStore()
	tier := starterQuotas()
	// Seed enough inert bytes to trip the reserve-max check.
	store.seedUpload(db.Upload{
		ID:         "upl_seed_1",
		TeamID:     "team-1",
		Status:     "received",
		SizeBytes:  tier.MaxInertBytes - DefaultUploadMaxArchiveBytes + 1,
		ReceivedAt: time.Now(),
	})

	fi := &fakeIngest{}
	srv := newUploadRouterWithQuotaStore(t, store, fi, nil)

	archive := makeTarZst(t, "hello.txt", "hello")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422; body: %s", resp.StatusCode, raw)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != apperror.CodeTeamQuotaExceeded {
		t.Errorf("error code = %q, want %q", code, apperror.CodeTeamQuotaExceeded)
	}
}

func TestUploadsPost_QuotaConcurrentPinned(t *testing.T) {
	store, token := newFakeStore()
	tier := starterQuotas()
	// Seed exactly MaxConcurrentPinned pinned uploads.
	for i := 0; i < tier.MaxConcurrentPinned; i++ {
		store.seedUpload(db.Upload{
			ID:         fmt.Sprintf("upl_pin_%03d", i),
			TeamID:     "team-1",
			Status:     "pinned",
			SizeBytes:  1,
			ReceivedAt: time.Now(),
		})
	}

	fi := &fakeIngest{}
	srv := newUploadRouterWithQuotaStore(t, store, fi, nil)

	archive := makeTarZst(t, "hello.txt", "hello")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422; body: %s", resp.StatusCode, raw)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != apperror.CodeTeamQuotaExceeded {
		t.Errorf("error code = %q, want %q", code, apperror.CodeTeamQuotaExceeded)
	}
}

func TestUploadsPost_QuotaDailyUploads(t *testing.T) {
	store, token := newFakeStore()
	tier := starterQuotas()
	// Seed MaxDailyUploads rows all received within the last hour (within the 24h window).
	for i := 0; i < tier.MaxDailyUploads; i++ {
		store.seedUpload(db.Upload{
			ID:         fmt.Sprintf("upl_daily_%04d", i),
			TeamID:     "team-1",
			Status:     "received",
			SizeBytes:  1,
			ReceivedAt: time.Now().Add(-time.Hour), // 1h ago is within 24h window
		})
	}

	fi := &fakeIngest{}
	srv := newUploadRouterWithQuotaStore(t, store, fi, nil)

	archive := makeTarZst(t, "hello.txt", "hello")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422; body: %s", resp.StatusCode, raw)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != apperror.CodeTeamQuotaExceeded {
		t.Errorf("error code = %q, want %q", code, apperror.CodeTeamQuotaExceeded)
	}
}

func TestUploadsPost_QuotaCheckHappensBeforeMultipart(t *testing.T) {
	// Even an invalid multipart body → quota check fires first → 422 team_quota_exceeded
	store, token := newFakeStore()
	tier := starterQuotas()
	// Trip the concurrent-pinned cap.
	for i := 0; i < tier.MaxConcurrentPinned; i++ {
		store.seedUpload(db.Upload{
			ID:         fmt.Sprintf("upl_pre_%03d", i),
			TeamID:     "team-1",
			Status:     "pinned",
			SizeBytes:  1,
			ReceivedAt: time.Now(),
		})
	}

	fi := &fakeIngest{}
	srv := newUploadRouterWithQuotaStore(t, store, fi, nil)

	// Deliberately invalid body — not multipart at all.
	body := strings.NewReader("not-a-multipart-body")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=fakeboundary")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Quota fires before multipart parse → 422, not 400
	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422 (quota before multipart); body: %s", resp.StatusCode, raw)
	}
	var errBody map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	code, _ := errBody["error"].(map[string]any)["code"].(string)
	if code != apperror.CodeTeamQuotaExceeded {
		t.Errorf("error code = %q, want %q (quota check must fire before multipart parse)", code, apperror.CodeTeamQuotaExceeded)
	}
}

func TestUploadsPost_ZeroStateStoreAllowsUpload(t *testing.T) {
	// The store is non-nil but returns zeros (no prior uploads, no pinned, no daily).
	// True nil-store bypass is covered by TestCheckUploadQuota_NilStoreSkipsCheck.
	// newUploadRouter passes store (which has the quota methods) but the
	// store returns zeros → no quota violations → existing behavior preserved.
	archive := makeTarZst(t, "hello.txt", "hello world")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201 (nil quota store should not block); body: %s", resp.StatusCode, raw)
	}
}

func TestUploadsArchiveGetReadFailureEmitsFailureAudit(t *testing.T) {
	store, signer, arc, fa, srv := newArchiveTestEnv(t)

	// Seed the DB row so auth/lookup succeeds, but configure the archive reader
	// to return an error when Archive() is called.
	uploadID := "upl_readfail"
	u := db.Upload{
		ID: uploadID, TeamID: "team-1",
		SizeBytes: 42, ArchiveFormat: "tar.zst", Status: "received",
	}
	store.seedUpload(u)
	// Do NOT seed arc.contents — instead set an explicit error.
	arc.err = errors.New("simulated disk failure")

	tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: uploadID})
	req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, uploadID), nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// Handler must return 500 internal_error.
	if resp.StatusCode != http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 500; body: %s", resp.StatusCode, body)
	}

	// Failure audit must have been written with the unified action name.
	if len(fa.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(fa.entries))
	}
	entry := fa.entries[0]
	if entry.Action != "app_source.upload.archive_downloaded" {
		t.Errorf("audit action = %q, want app_source.upload.archive_downloaded", entry.Action)
	}
	if entry.Outcome != audit.OutcomeFailure {
		t.Errorf("audit outcome = %q, want failure", entry.Outcome)
	}
	if entry.HTTPStatus == nil || *entry.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("audit HTTPStatus = %v, want 500", entry.HTTPStatus)
	}
	if entry.SubjectID == nil || *entry.SubjectID != uploadID {
		t.Errorf("audit SubjectID = %v, want %q", entry.SubjectID, uploadID)
	}

	// DB row must remain untouched (still "received").
	row, ok := store.uploadRows[0], len(store.uploadRows) > 0
	if !ok {
		t.Fatal("upload row missing from store")
	}
	if row.Status != "received" {
		t.Errorf("upload status = %q, want received (DB untouched by failed read)", row.Status)
	}
}

// ─── T21 metric recorder tests ───────────────────────────────────────────────

// withUploadSuccessRecorder temporarily swaps recordUploadSuccessMetric and
// restores it via t.Cleanup. The recorder captures the last call's arguments.
func withUploadSuccessRecorder(t *testing.T) *struct {
	called   bool
	size     int64
	duration time.Duration
} {
	t.Helper()
	got := &struct {
		called   bool
		size     int64
		duration time.Duration
	}{}
	orig := recordUploadSuccessMetric
	recordUploadSuccessMetric = func(sz int64, d time.Duration) {
		got.called = true
		got.size = sz
		got.duration = d
	}
	t.Cleanup(func() { recordUploadSuccessMetric = orig })
	return got
}

func withUploadRejectionRecorder(t *testing.T) *struct {
	called bool
	reason string
} {
	t.Helper()
	got := &struct {
		called bool
		reason string
	}{}
	orig := recordUploadRejectionMetric
	recordUploadRejectionMetric = func(r string) {
		got.called = true
		got.reason = r
	}
	t.Cleanup(func() { recordUploadRejectionMetric = orig })
	return got
}

func withQuotaRejectionRecorder(t *testing.T) *struct {
	called bool
	reason string
} {
	t.Helper()
	got := &struct {
		called bool
		reason string
	}{}
	orig := recordQuotaRejectionMetric
	recordQuotaRejectionMetric = func(r string) {
		got.called = true
		got.reason = r
	}
	t.Cleanup(func() { recordQuotaRejectionMetric = orig })
	return got
}

func TestUploadsPost_RecordsSuccessMetric(t *testing.T) {
	rec := withUploadSuccessRecorder(t)

	archive := makeTarZst(t, "hello.txt", "hello world")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body: %s", resp.StatusCode, raw)
	}

	if !rec.called {
		t.Error("recordUploadSuccessMetric was not called on success")
	}
	if rec.size <= 0 {
		t.Errorf("success metric size = %d, want > 0", rec.size)
	}
	if rec.duration < 0 {
		t.Errorf("success metric duration = %v, want >= 0", rec.duration)
	}
}

func TestUploadsPost_RecordsRejectionOnArchiveCorrupt(t *testing.T) {
	rec := withUploadRejectionRecorder(t)

	// Bytes that match path-escape trigger in fakeIngest.
	corruptSentinel := []byte("\x28\xb5\x2f\xfd__path_escape__")
	fi := &fakeIngest{corruptPrefix: corruptSentinel}
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, corruptSentinel, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	if !rec.called {
		t.Error("recordUploadRejectionMetric was not called on archive_corrupt path")
	}
	if rec.reason != "archive_corrupt" {
		t.Errorf("rejection reason = %q, want archive_corrupt", rec.reason)
	}
}

func TestUploadsPost_RecordsRejectionOnSHA256Mismatch(t *testing.T) {
	rec := withUploadRejectionRecorder(t)

	archive := makeTarZst(t, "f.txt", "hello")
	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	body, ct := buildMultipart(t, archive, strings.Repeat("a", 64))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
	if !rec.called {
		t.Error("recordUploadRejectionMetric was not called on sha256_mismatch path")
	}
	if rec.reason != "sha256_mismatch" {
		t.Errorf("rejection reason = %q, want sha256_mismatch", rec.reason)
	}
}

func TestUploadsPost_RecordsQuotaRejection(t *testing.T) {
	rec := withQuotaRejectionRecorder(t)

	store, token := newFakeStore()
	tier := starterQuotas()
	// Trip the concurrent-pinned cap.
	for i := 0; i < tier.MaxConcurrentPinned; i++ {
		store.seedUpload(db.Upload{
			ID:         fmt.Sprintf("upl_qrec_%03d", i),
			TeamID:     "team-1",
			Status:     "pinned",
			SizeBytes:  1,
			ReceivedAt: time.Now(),
		})
	}

	fi := &fakeIngest{}
	srv := newUploadRouterWithQuotaStore(t, store, fi, nil)

	archive := makeTarZst(t, "hello.txt", "hello")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422; body: %s", resp.StatusCode, raw)
	}
	if !rec.called {
		t.Error("recordQuotaRejectionMetric was not called on quota rejection")
	}
	if rec.reason != "pinned" {
		t.Errorf("quota rejection reason = %q, want pinned", rec.reason)
	}
}

func TestUploadsPost_RecordsQuotaRejectionAuditEvent(t *testing.T) {
	store, token := newFakeStore()
	tier := starterQuotas()
	// Trip the concurrent-pinned cap.
	for i := 0; i < tier.MaxConcurrentPinned; i++ {
		store.seedUpload(db.Upload{
			ID:         fmt.Sprintf("upl_qaev_%03d", i),
			TeamID:     "team-1",
			Status:     "pinned",
			SizeBytes:  1,
			ReceivedAt: time.Now(),
		})
	}

	fa := &fakeUploadAudit{}
	fi := &fakeIngest{}
	srv := newUploadRouterWithQuotaStore(t, store, fi, fa)

	archive := makeTarZst(t, "hello.txt", "hello")
	body, ct := buildMultipart(t, archive, "")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ct)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 422; body: %s", resp.StatusCode, raw)
	}

	if len(fa.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(fa.entries))
	}
	entry := fa.entries[0]
	if entry.Action != "app_source.upload.quota_rejected" {
		t.Errorf("audit action = %q, want app_source.upload.quota_rejected", entry.Action)
	}
	if entry.Outcome != audit.OutcomeFailure {
		t.Errorf("audit outcome = %q, want failure", entry.Outcome)
	}
	if entry.SubjectID != nil {
		t.Errorf("audit SubjectID should be nil for rejected upload (no row created), got %v", entry.SubjectID)
	}
	if entry.TeamID != "team-1" {
		t.Errorf("audit TeamID = %q, want team-1", entry.TeamID)
	}
	result, _ := entry.Result.(map[string]any)
	if result["reason"] != "pinned" {
		t.Errorf("audit result reason = %v, want pinned", result["reason"])
	}
	if entry.Source != audit.SourceUser {
		t.Errorf("audit source = %q, want user", entry.Source)
	}
}

// ─── T21 archive download outcome metrics ─────────────────────────────────

// TestUploadsPost_RecordsRejectionOnMissingArchive verifies that the upload
// handler emits a "missing_archive" rejection metric when the multipart body
// contains no archive part (Fix 4 of the T21 metrics audit).
func TestUploadsPost_RecordsRejectionOnMissingArchive(t *testing.T) {
	rec := withUploadRejectionRecorder(t)

	fi := &fakeIngest{}
	srv, token, store := newUploadRouter(t, fi, nil)

	// Build a multipart body with only a non-archive field (no "archive" part).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormField("other_field")
	if err != nil {
		t.Fatalf("create form field: %v", err)
	}
	if _, err := w.Write([]byte("ignored")); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/teams/"+store.team.Slug+"/uploads", &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, raw)
	}
	if !rec.called {
		t.Error("recordUploadRejectionMetric was not called on missing_archive path")
	}
	if rec.reason != "missing_archive" {
		t.Errorf("rejection reason = %q, want missing_archive", rec.reason)
	}
}

// TestUploadsArchiveGet_RecordsDistinctOutcomeMetrics verifies that
// uploadArchiveHandler emits a distinct outcome label for each failure path
// as required by the metrics spec (Issue 2 + 3 of the T21 compliance audit).
func TestUploadsArchiveGet_RecordsDistinctOutcomeMetrics(t *testing.T) {
	type archiveCase struct {
		name        string
		buildReq    func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request
		wantOutcome string
	}

	cases := []archiveCase{
		{
			name: "success",
			buildReq: func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request {
				u := db.Upload{ID: "upl_m_ok", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "received"}
				seedArchiveUpload(store, arc, u, []byte("data"))
				tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_m_ok"})
				req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_m_ok"), nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				return req
			},
			wantOutcome: "success",
		},
		{
			name: "unauthorized_no_header",
			buildReq: func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request {
				req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_m_noheader"), nil)
				// deliberately no Authorization header
				return req
			},
			wantOutcome: "unauthorized",
		},
		{
			name: "forbidden_url_mismatch",
			buildReq: func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request {
				u := db.Upload{ID: "upl_m_A", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "received"}
				seedArchiveUpload(store, arc, u, []byte("data"))
				// Token claims upload A but URL references upload B
				tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_m_A"})
				req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_m_B"), nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				return req
			},
			wantOutcome: "forbidden",
		},
		{
			name: "not_found_cross_team",
			buildReq: func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request {
				// Upload belongs to team-2, token claims team-1 → 404
				u := db.Upload{ID: "upl_m_cross", TeamID: "team-2", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "received"}
				seedArchiveUpload(store, arc, u, []byte("data"))
				tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_m_cross"})
				req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_m_cross"), nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				return req
			},
			wantOutcome: "not_found",
		},
		{
			name: "expired_status",
			buildReq: func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request {
				u := db.Upload{ID: "upl_m_exp", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "expired"}
				seedArchiveUpload(store, arc, u, []byte("data"))
				tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_m_exp"})
				req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_m_exp"), nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				return req
			},
			wantOutcome: "expired",
		},
		{
			name: "internal_error_archive_read_fail",
			buildReq: func(store *fakeStore, signer *ingestion.TokenSigner, arc *fakeArchiveReader, srv *httptest.Server) *http.Request {
				u := db.Upload{ID: "upl_m_ioerr", TeamID: "team-1", SizeBytes: 4, ArchiveFormat: "tar.zst", Status: "received"}
				store.seedUpload(u)
				// Seed the DB row but NOT the archive bytes → Archive() returns ErrUploadNotFound
				// which is treated as an internal read failure (not a DB-level not_found).
				// We inject a hard error instead via arc.err.
				arc.err = errors.New("simulated storage failure")
				req, _ := http.NewRequest(http.MethodGet, archiveGetURL(srv, "upl_m_ioerr"), nil)
				tok := mintArchiveToken(t, signer, ingestion.TokenClaims{TeamID: "team-1", UploadID: "upl_m_ioerr"})
				req.Header.Set("Authorization", "Bearer "+tok)
				return req
			},
			wantOutcome: "internal_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, signer, arc, _, srv := newArchiveTestEnv(t)

			var recorded []string
			BindUploadMetrics(
				func(int64, time.Duration) {},
				func(string) {},
				func(string) {},
				func(o string) { recorded = append(recorded, o) },
			)
			t.Cleanup(func() {
				BindUploadMetrics(nil, nil, nil, nil)
			})

			req := tc.buildReq(store, signer, arc, srv)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()

			if len(recorded) != 1 {
				t.Fatalf("archive metric recorded %d times, want 1; outcomes: %v", len(recorded), recorded)
			}
			if recorded[0] != tc.wantOutcome {
				t.Errorf("archive outcome = %q, want %q", recorded[0], tc.wantOutcome)
			}
		})
	}
}
