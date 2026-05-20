package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// sourceTestServer builds an httptest.Server that handles:
//   - POST /v1/teams/{team}/uploads  → responds with a fixed UploadResponse
//   - POST /v1/teams/{team}/apps:preview → records request body, responds with PreviewResponse
//   - POST /v1/teams/{team}/apps  → responds with AppCreateResponse
//
// capturedPreview is filled by the preview handler so callers can inspect the
// AppCreateRequest that was sent.
func sourceTestServer(t *testing.T, teamSlug string) (srv *httptest.Server, capturedPreview *dto.AppCreateRequest) {
	t.Helper()
	captured := &dto.AppCreateRequest{}
	mux := http.NewServeMux()

	uploadPath := "/v1/teams/" + teamSlug + "/uploads"
	mux.HandleFunc(uploadPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Drain multipart body so packer goroutine can finish.
		_, _ = io.Copy(io.Discard, r.Body)
		resp := dto.UploadResponse{
			UploadID:   "upl_test123",
			TeamID:     "team-test",
			SizeBytes:  512,
			SHA256:     "abcdef1234",
			Format:     "tar.zst",
			ReceivedAt: time.Now().UTC(),
			ExpiresAt:  time.Now().UTC().Add(24 * time.Hour),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})

	previewPath := "/v1/teams/" + teamSlug + "/apps:preview"
	mux.HandleFunc(previewPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req dto.AppCreateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*captured = req
		resp := dto.PreviewResponse{
			PreviewID: "preview-src-1",
			Action:    "create_app",
			Summary:   "create app " + req.Slug,
			ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	confirmPath := "/v1/teams/" + teamSlug + "/apps"
	mux.HandleFunc(confirmPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := dto.AppCreateResponse{
			AppID:         "app-test",
			AppSlug:       "demo",
			DeployRunID:   "deploy-test",
			TraceID:       "trace-test",
			SubdomainURL:  "https://demo.0ops.dev",
			InitialDeploy: true,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	})

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, captured
}

// runCreateCmd executes `0ops apps create` with the given extra args through
// the sourceTestServer and returns (captured preview request, stderr, error).
func runCreateCmd(t *testing.T, teamSlug, token string, srvURL string, extraArgs ...string) (captured *dto.AppCreateRequest, errOut string, execErr error) {
	t.Helper()
	var srv *httptest.Server
	srv, captured = sourceTestServer(t, teamSlug)
	if srvURL == "" {
		srvURL = srv.URL
	}

	baseArgs := []string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srvURL,
		"--token", token,
		"--yes",
		"--output", "json",
	}
	cmd := NewRootCommand()
	cmd.SetArgs(append(baseArgs, extraArgs...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	execErr = cmd.ExecuteContext(context.Background())
	return captured, stderr.String(), execErr
}

// sourceTestToken returns a valid bearer token for tests that use
// sourceTestServer (which doesn't validate auth — any non-empty token passes).
func sourceTestToken() string {
	// The sourceTestServer doesn't validate auth; any non-empty value works.
	return "op_device_testtoken123"
}

// TestAppsCreate_SourceGitHubURL verifies that --source https://github.com/...
// sets source.type=github in the preview request and skips upload.
func TestAppsCreate_SourceGitHubURL(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, captured := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", "https://github.com/foo/bar",
		"--ref", "main",
		"--yes",
		"--output", "json",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v\nstderr: %s", err, stderr.String())
	}

	if captured.Source == nil {
		t.Fatal("expected source to be set, got nil")
	}
	if captured.Source.Type != dto.SourceKindGitHub {
		t.Errorf("source.type = %q, want %q", captured.Source.Type, dto.SourceKindGitHub)
	}
	if captured.Source.GitHub == nil {
		t.Fatal("expected source.github to be set")
	}
	if captured.Source.GitHub.URL != "https://github.com/foo/bar" {
		t.Errorf("source.github.url = %q, want %q", captured.Source.GitHub.URL, "https://github.com/foo/bar")
	}
	if captured.Source.GitHub.Ref != "main" {
		t.Errorf("source.github.ref = %q, want main", captured.Source.GitHub.Ref)
	}
	if captured.RepoURL != "" {
		t.Errorf("expected legacy RepoURL to be empty, got %q", captured.RepoURL)
	}
}

// TestAppsCreate_SourceUploadID verifies that --source upload://upl_xxx
// sets source.type=upload without triggering a local upload.
func TestAppsCreate_SourceUploadID(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, captured := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", "upload://upl_xxx",
		"--yes",
		"--output", "json",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v\nstderr: %s", err, stderr.String())
	}

	if captured.Source == nil {
		t.Fatal("expected source to be set, got nil")
	}
	if captured.Source.Type != dto.SourceKindUpload {
		t.Errorf("source.type = %q, want %q", captured.Source.Type, dto.SourceKindUpload)
	}
	if captured.Source.Upload == nil {
		t.Fatal("expected source.upload to be set")
	}
	if captured.Source.Upload.UploadID != "upl_xxx" {
		t.Errorf("source.upload.upload_id = %q, want %q", captured.Source.Upload.UploadID, "upl_xxx")
	}
}

// TestAppsCreate_SourceLocalPath_UploadsAndCreates verifies the happy path for
// --source ./tmpdir: the CLI packs + uploads, then sends a preview request with
// source.type=upload and the upload_id returned by the server.
func TestAppsCreate_SourceLocalPath_UploadsAndCreates(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, captured := sourceTestServer(t, teamSlug)

	// Create a real temporary directory with a file in it.
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", dir,
		"--yes",
		"--output", "json",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v\nstderr: %s", err, stderr.String())
	}

	// Verify stderr mentions upload progress.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "Packing") {
		t.Errorf("expected 'Packing' in stderr, got: %s", stderrStr)
	}
	if !strings.Contains(stderrStr, "upl_test123") {
		t.Errorf("expected upload ID in stderr, got: %s", stderrStr)
	}

	// Verify preview request has source.type=upload with the server-returned ID.
	if captured.Source == nil {
		t.Fatal("expected source to be set in preview request")
	}
	if captured.Source.Type != dto.SourceKindUpload {
		t.Errorf("source.type = %q, want %q", captured.Source.Type, dto.SourceKindUpload)
	}
	if captured.Source.Upload == nil {
		t.Fatal("expected source.upload to be set")
	}
	if captured.Source.Upload.UploadID != "upl_test123" {
		t.Errorf("upload_id = %q, want upl_test123", captured.Source.Upload.UploadID)
	}
}

// TestAppsCreate_SourceFileURL verifies that file:// goes through the legacy
// RepoURL path (Source is nil; RepoURL is set).
func TestAppsCreate_SourceFileURL(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, captured := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", "file:///workspace/app",
		"--ref", "main",
		"--yes",
		"--output", "json",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v\nstderr: %s", err, stderr.String())
	}

	// file:// goes through legacy path: Source is nil, RepoURL is set.
	if captured.Source != nil {
		t.Errorf("expected Source to be nil for file:// path, got %+v", captured.Source)
	}
	if captured.RepoURL != "file:///workspace/app" {
		t.Errorf("RepoURL = %q, want file:///workspace/app", captured.RepoURL)
	}
}

// TestAppsCreate_RepoURLDeprecationWarning verifies that using --repo-url
// alone prints a deprecation warning to stderr but still proceeds.
func TestAppsCreate_RepoURLDeprecationWarning(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, captured := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--repo-url", "https://github.com/foo/bar",
		"--ref", "main",
		"--yes",
		"--output", "json",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v\nstderr: %s", err, stderr.String())
	}

	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation warning in stderr, got: %s", stderr.String())
	}
	// Verify the request still went through.
	if captured.RepoURL != "https://github.com/foo/bar" {
		t.Errorf("RepoURL = %q, want https://github.com/foo/bar", captured.RepoURL)
	}
}

// TestAppsCreate_SourceAndRepoURLConflict verifies that setting both
// --source and --repo-url returns an error.
func TestAppsCreate_SourceAndRepoURLConflict(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, _ := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", "./mydir",
		"--repo-url", "https://github.com/foo/bar",
		"--yes",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for --source + --repo-url conflict, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in error, got: %v", err)
	}
}

// TestAppsCreate_SourceMissing verifies that passing neither --source nor
// --repo-url returns an error.
func TestAppsCreate_SourceMissing(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, _ := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--yes",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error when neither --source nor --repo-url is set")
	}
	if !strings.Contains(err.Error(), "--source") {
		t.Errorf("expected '--source' mention in error, got: %v", err)
	}
}

// TestAppsCreate_LocalPathDoesntExist verifies that a non-existent local path
// returns an error before any HTTP requests are made.
func TestAppsCreate_LocalPathDoesntExist(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()
	srv, _ := sourceTestServer(t, teamSlug)

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", "./does-not-exist-xyz",
		"--yes",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
	if !strings.Contains(err.Error(), "source path") {
		t.Errorf("expected 'source path' in error, got: %v", err)
	}
}

// TestAppsCreate_UploadError413 verifies that an HTTP 413 from the upload
// endpoint returns a friendly error message containing ".dockerignore".
func TestAppsCreate_UploadError413(t *testing.T) {
	teamSlug := "test-team"
	token := sourceTestToken()

	// Build a custom server that returns 413 for upload.
	mux := http.NewServeMux()
	uploadPath := "/v1/teams/" + teamSlug + "/uploads"
	mux.HandleFunc(uploadPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "payload_too_large",
				"message": "archive exceeds 100 MiB limit",
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Create a real temporary directory with a file.
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := NewRootCommand()
	cmd.SetArgs([]string{
		"apps", "create",
		"--team", teamSlug,
		"--host", srv.URL,
		"--token", token,
		"--slug", "demo",
		"--source", dir,
		"--yes",
	})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for 413 response, got nil")
	}
	if !strings.Contains(err.Error(), ".dockerignore") {
		t.Errorf("expected '.dockerignore' hint in error, got: %v", err)
	}
}
