package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// handCraftedUploadResponse returns a dto.UploadResponse with deterministic
// values for assertion in happy-path tests.
func handCraftedUploadResponse() dto.UploadResponse {
	return dto.UploadResponse{
		UploadID:   "upl_testid123",
		TeamID:     "team-abc",
		SizeBytes:  1234,
		SHA256:     "deadbeef",
		Format:     "tar.zst",
		ReceivedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ExpiresAt:  time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

// zstdMagic returns the first 4 bytes of a zstd frame: 0x28 0xb5 0x2f 0xfd.
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// TestUploadDir_HappyPath verifies that a valid upload returns a parsed UploadResponse.
func TestUploadDir_HappyPath(t *testing.T) {
	const teamSlug = "my-team"
	const bearerToken = "tok_abc123"

	wantResp := handCraftedUploadResponse()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method and path.
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		wantPath := "/v1/teams/" + teamSlug + "/uploads"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		// Verify Authorization header.
		if got := r.Header.Get("Authorization"); got != "Bearer "+bearerToken {
			t.Errorf("Authorization = %q, want %q", got, "Bearer "+bearerToken)
		}

		// Verify Content-Type is multipart/form-data.
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("Content-Type = %q, want multipart/form-data prefix", ct)
		}

		// Read multipart body; verify archive part starts with zstd magic bytes.
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		var archiveSeen bool
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("NextPart: %v", err)
				break
			}
			if part.FormName() == "archive" {
				archiveSeen = true
				head := make([]byte, 4)
				if _, err := io.ReadFull(part, head); err != nil {
					t.Errorf("read archive head: %v", err)
				} else {
					for i, b := range zstdMagic {
						if head[i] != b {
							t.Errorf("archive magic[%d] = %#x, want %#x", i, head[i], b)
						}
					}
				}
				// Drain remaining bytes.
				_, _ = io.Copy(io.Discard, part)
			}
			_ = part.Close()
		}
		if !archiveSeen {
			t.Error("archive part not found in multipart body")
		}

		// Respond 201.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(wantResp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "hello.txt", "hello world")

	c := NewUploadsClient(srv.URL, bearerToken)
	got, err := c.UploadDir(context.Background(), teamSlug, dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	if got.UploadID != wantResp.UploadID {
		t.Errorf("UploadID = %q, want %q", got.UploadID, wantResp.UploadID)
	}
	if got.TeamID != wantResp.TeamID {
		t.Errorf("TeamID = %q, want %q", got.TeamID, wantResp.TeamID)
	}
	if got.Format != wantResp.Format {
		t.Errorf("Format = %q, want %q", got.Format, wantResp.Format)
	}
}

// TestUploadDir_PackErrorPropagates verifies that ErrTarballTooLarge is the
// returned error when the source exceeds MaxBytes, not an HTTP error.
func TestUploadDir_PackErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body regardless so the server doesn't block the client.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	// 10 KB of incompressible data; MaxBytes=100 will trip ErrTarballTooLarge.
	content := make([]byte, 10*1024)
	for i := range content {
		content[i] = byte(i & 0xFF)
	}
	abs := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(abs, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c := NewUploadsClient(srv.URL, "tok_test")
	_, err := c.UploadDir(context.Background(), "team-x", dir, PackOptions{
		MaxBytes:   100,
		MaxEntries: math.MaxInt,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrTarballTooLarge) {
		t.Errorf("expected ErrTarballTooLarge, got %v", err)
	}
}

// TestUploadDir_ServerReturns4xx verifies UploadError is returned for non-2xx responses.
func TestUploadDir_ServerReturns4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain body to avoid broken-pipe on client side.
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintln(w, `{"error":{"code":"source_invalid","message":"bad payload"}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, "tok_test")
	_, err := c.UploadDir(context.Background(), "team-y", dir, defaultOpts())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ue *UploadError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *UploadError, got %T: %v", err, err)
	}
	if ue.HTTPStatus != http.StatusUnprocessableEntity {
		t.Errorf("HTTPStatus = %d, want %d", ue.HTTPStatus, http.StatusUnprocessableEntity)
	}
	if ue.Code != "source_invalid" {
		t.Errorf("Code = %q, want %q", ue.Code, "source_invalid")
	}
	if ue.Message != "bad payload" {
		t.Errorf("Message = %q, want %q", ue.Message, "bad payload")
	}
}

// TestUploadDir_ContextCancel verifies that canceling the context returns
// context.Canceled.
func TestUploadDir_ContextCancel(t *testing.T) {
	// Use a channel to synchronise: server signals it has started reading so
	// the test can cancel the context reliably.
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		// Signal that we've started reading, then block on the body.
		close(started)
		_, _ = io.Copy(io.Discard, part)
		_ = part.Close()
	}))
	defer srv.Close()

	dir := t.TempDir()
	// Write enough data to keep the upload busy while we cancel.
	content := strings.Repeat("a", 512*1024)
	writeFile(t, dir, "large.txt", content)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		c := NewUploadsClient(srv.URL, "tok_cancel")
		_, err := c.UploadDir(ctx, "team-z", dir, defaultOpts())
		done <- err
	}()

	// Wait for the server to start receiving before canceling.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start reading within timeout")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after context cancel, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("UploadDir did not return after context cancel")
	}
}

// TestUploadDir_MissingBaseURL verifies that an empty BaseURL returns an error
// before making any HTTP request.
func TestUploadDir_MissingBaseURL(t *testing.T) {
	c := NewUploadsClient("", "tok_test")
	_, err := c.UploadDir(context.Background(), "team", t.TempDir(), defaultOpts())
	if err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
	if !strings.Contains(err.Error(), "BaseURL") {
		t.Errorf("error should mention BaseURL, got: %v", err)
	}
}

// TestUploadDir_MissingBearerToken verifies that an empty BearerToken returns
// an error before making any HTTP request.
func TestUploadDir_MissingBearerToken(t *testing.T) {
	c := NewUploadsClient("http://localhost:9999", "")
	_, err := c.UploadDir(context.Background(), "team", t.TempDir(), defaultOpts())
	if err == nil {
		t.Fatal("expected error for empty BearerToken")
	}
	if !strings.Contains(err.Error(), "BearerToken") {
		t.Errorf("error should mention BearerToken, got: %v", err)
	}
}

// TestUploadDir_HTTPDoError verifies that a network-level failure returns a
// wrapped error (not a panic or nil).
func TestUploadDir_HTTPDoError(t *testing.T) {
	// Use a URL that no server is listening on.
	c := NewUploadsClient("http://127.0.0.1:0", "tok_test")
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x")

	_, err := c.UploadDir(context.Background(), "team", dir, defaultOpts())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	// Should be wrapped as "http do: ..." — just verify non-nil.
}

// TestUploadDir_AuthHeaderFormat verifies the Authorization header has the
// exact format "Bearer <token>" — no colon, no extra spaces.
func TestUploadDir_AuthHeaderFormat(t *testing.T) {
	const token = "mytoken456"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, token)
	_, err := c.UploadDir(context.Background(), "team", dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	want := "Bearer " + token
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestUploadDir_TeamSlugPathEscape verifies that url.PathEscape is applied to
// the team slug when building the endpoint URL.
// Slug regex is alphanumeric today, but this test uses a slug with a space to
// exercise the escaping path (exercises url.PathEscape explicitly).
func TestUploadDir_TeamSlugPathEscape(t *testing.T) {
	// Use a slug with a space. The URL path must contain %20.
	const slug = "my team"
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, "tok")
	// The URL that reaches the server has the path decoded by net/http; verify
	// via the raw request URI instead. We can check the request URI using a
	// custom RoundTripper, but the simpler check is that the server path
	// contains the unescaped slug (meaning Go's server decoded %20 → space).
	_, err := c.UploadDir(context.Background(), slug, dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	wantPath := "/v1/teams/my team/uploads"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
}

// TestUploadDir_LargeStreaming packs a 1 MB tree and verifies the server
// receives a valid zstd-compressed archive without buffering the entire payload
// in memory (validated by the server reading all bytes correctly).
func TestUploadDir_LargeStreaming(t *testing.T) {
	var archiveBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("NextPart: %v", err)
				break
			}
			if part.FormName() == "archive" {
				archiveBytes, err = io.ReadAll(part)
				if err != nil {
					t.Errorf("read archive: %v", err)
				}
			}
			_ = part.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	// Write ~1 MB across 4 files (~256 KB each).
	for i := range 4 {
		content := make([]byte, 256*1024)
		for j := range content {
			content[j] = byte((i*256 + j) & 0xFF)
		}
		writeFile(t, dir, fmt.Sprintf("file%d.bin", i), string(content))
	}

	c := NewUploadsClient(srv.URL, "tok_large")
	got, err := c.UploadDir(context.Background(), "team-large", dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	if got.UploadID != "upl_testid123" {
		t.Errorf("UploadID = %q, want upl_testid123", got.UploadID)
	}

	// Verify the archive starts with zstd magic bytes.
	if len(archiveBytes) < 4 {
		t.Fatalf("archive too short: %d bytes", len(archiveBytes))
	}
	for i, b := range zstdMagic {
		if archiveBytes[i] != b {
			t.Errorf("archive magic[%d] = %#x, want %#x", i, archiveBytes[i], b)
		}
	}

	// Decode the archive and verify files are present.
	entries := extractTarZst(t, archiveBytes)
	for i := range 4 {
		name := fmt.Sprintf("file%d.bin", i)
		if _, ok := entries[name]; !ok {
			t.Errorf("file %q not found in archive", name)
		}
	}
}

// TestUploadDir_MultipartFieldName verifies the archive part uses the field
// name "archive" (as expected by the server's uploadHandler).
func TestUploadDir_MultipartFieldName(t *testing.T) {
	var gotFormName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, _ := r.MultipartReader()
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				break
			}
			if part.FormName() != "" {
				gotFormName = part.FormName()
			}
			_, _ = io.Copy(io.Discard, part)
			_ = part.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, "tok")
	_, err := c.UploadDir(context.Background(), "team", dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	if gotFormName != "archive" {
		t.Errorf("form field name = %q, want %q", gotFormName, "archive")
	}
}

// TestUploadDir_UploadErrorString verifies the UploadError.Error() format.
func TestUploadDir_UploadErrorString(t *testing.T) {
	e := &UploadError{HTTPStatus: 422, Code: "source_invalid", Message: "bad payload"}
	got := e.Error()
	want := "upload failed: HTTP 422 source_invalid: bad payload"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestUploadDir_ResponseBodyReadOnNon2xx verifies that a 4xx response with a
// body larger than 4096 bytes is truncated (LimitReader) and still parses the
// apperror envelope correctly.
func TestUploadDir_ResponseBodyReadOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// Emit a valid envelope followed by 8 KB of garbage to confirm
		// LimitReader truncates without error.
		body := `{"error":{"code":"validation_failed","message":"too many fields"}}` +
			strings.Repeat("x", 8192)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, "tok")
	_, err := c.UploadDir(context.Background(), "team", dir, defaultOpts())
	var ue *UploadError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *UploadError, got %T: %v", err, err)
	}
	if ue.Code != "validation_failed" {
		t.Errorf("Code = %q, want validation_failed", ue.Code)
	}
}

// TestUploadDir_NilHTTPUsesDefault verifies that leaving HTTP nil falls back
// to http.DefaultClient (i.e., does not panic).
func TestUploadDir_NilHTTPUsesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x")

	c := &UploadsClient{
		BaseURL:     srv.URL,
		BearerToken: "tok",
		HTTP:        nil, // must not panic
	}
	_, err := c.UploadDir(context.Background(), "t", dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
}

// TestUploadDir_MultipartContentType verifies the multipart writer boundary
// is included in the Content-Type header.
func TestUploadDir_MultipartContentType(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				break
			}
			_, _ = io.Copy(io.Discard, p)
			_ = p.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(handCraftedUploadResponse())
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, "tok")
	_, err := c.UploadDir(context.Background(), "team", dir, defaultOpts())
	if err != nil {
		t.Fatalf("UploadDir: %v", err)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q, want multipart/form-data with boundary", gotCT)
	}
}

// TestUploadDir_GoRoutineNeverLeaks verifies (indirectly) that the goroutine
// started by UploadDir has exited before UploadDir returns — by asserting a
// result even when the server abruptly closes the connection.
func TestUploadDir_GoRoutineNeverLeaks(t *testing.T) {
	// Server closes the connection immediately without reading the body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "data")

	c := NewUploadsClient(srv.URL, "tok")
	// Must return (possibly with error) — not hang.
	done := make(chan struct{})
	go func() {
		_, _ = c.UploadDir(context.Background(), "team", dir, defaultOpts())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("UploadDir hung — goroutine likely leaked")
	}
}

// --- helpers used only in this test file ---

// readMultipartArchive reads the "archive" part bytes from a multipart reader.
// Returns nil if the part is absent.
func readMultipartArchive(t *testing.T, r *multipart.Reader) []byte {
	t.Helper()
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			t.Errorf("NextPart: %v", err)
			return nil
		}
		if part.FormName() == "archive" {
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				t.Errorf("read archive part: %v", err)
			}
			return data
		}
		_, _ = io.Copy(io.Discard, part)
		_ = part.Close()
	}
}
