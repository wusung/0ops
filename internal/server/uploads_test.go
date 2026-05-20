package server

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

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

// newUploadRouter wires a Router+fakeStore pair for upload handler tests.
// The returned triple (server, token, store) provides everything a test needs.
func newUploadRouter(t *testing.T, ingest ingestionStore, auditSvc uploadAuditWriter) (*httptest.Server, string, *fakeStore) {
	t.Helper()
	store, token := newFakeStore()
	// Inline a local newRouterFull call: we reuse NewRouter but also need to
	// inject uploadIngest and uploadAuditSvc. Use newRouterFull directly since
	// it is unexported but we're in the same package.
	h := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, ingest, auditSvc)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, token, store
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

	fi := &fakeIngest{}
	h := newRouterFull(store, newGitHubOAuthClient(), nil, nil, nil, nil, nil, nil, fi, nil)
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
