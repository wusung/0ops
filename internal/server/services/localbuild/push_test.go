package localbuild

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newUnixServer spins up an httptest-style server bound to a unix socket so
// PushViaSocket can be exercised end-to-end without a real podman daemon.
func newUnixServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "podman.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		_ = os.Remove(socketPath)
	})
	return socketPath
}

func TestPushViaSocket_Success(t *testing.T) {
	var gotPath string
	var gotRawPath string
	var gotAuth string
	socket := newUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawPath = r.RequestURI
		gotAuth = r.Header.Get("X-Registry-Auth")
		_, _ = fmt.Fprintln(w, `{"status":"Pushing"}`)
		_, _ = fmt.Fprintln(w, `{"status":"Layer pushed"}`)
		_, _ = fmt.Fprintln(w, `{"status":"Done"}`)
	}))

	err := PushViaSocket(context.Background(), socket, "localhost:5000/test:v1")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.HasPrefix(gotPath, "/v1.40/images/") || !strings.HasSuffix(gotPath, "/push") {
		t.Errorf("unexpected path %q", gotPath)
	}
	// Slashes inside imageRef must be percent-encoded on the wire so the
	// API treats the whole ref as a single path segment. (RFC 3986 leaves
	// `:` valid inside a segment so it is not escaped.)
	if !strings.Contains(gotRawPath, "localhost:5000%2Ftest:v1") {
		t.Errorf("imageRef `/` not escaped in raw path: %q", gotRawPath)
	}
	if gotAuth == "" {
		t.Errorf("X-Registry-Auth header missing")
	}
}

func TestPushViaSocket_StreamError(t *testing.T) {
	socket := newUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"status":"Pushing"}`)
		_, _ = fmt.Fprintln(w, `{"errorDetail":{"message":"unauthorized"},"error":"unauthorized"}`)
	}))
	err := PushViaSocket(context.Background(), socket, "localhost:5000/test:v1")
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestPushViaSocket_NonJSONLinesIgnored(t *testing.T) {
	socket := newUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `not json, just chatter`)
		_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
	}))
	err := PushViaSocket(context.Background(), socket, "x:y")
	if err != nil {
		t.Fatalf("expected success despite non-JSON line, got %v", err)
	}
}

func TestPushViaSocket_HTTPError(t *testing.T) {
	socket := newUnixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	err := PushViaSocket(context.Background(), socket, "x:y")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestPushViaSocket_MissingImageRef(t *testing.T) {
	socket := newUnixServer(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	err := PushViaSocket(context.Background(), socket, "")
	if err == nil {
		t.Fatal("expected error on empty imageRef")
	}
}
