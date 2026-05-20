package cli

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// extractTarZst decompresses a tar.zst stream and returns a map of path →
// content. Directories are mapped to empty string. Symlinks to "-> <target>".
func extractTarZst(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	result := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		switch hdr.Typeflag {
		case tar.TypeDir:
			result[name] = ""
		case tar.TypeSymlink:
			result[name] = "-> " + hdr.Linkname
		default:
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read entry %q: %v", name, err)
			}
			result[name] = string(body)
		}
	}
	return result
}

// defaultOpts returns PackOptions with no enforced caps.
func defaultOpts() PackOptions {
	return PackOptions{
		MaxBytes:   math.MaxInt64,
		MaxEntries: math.MaxInt,
	}
}

// sha256Hex returns the hex-encoded SHA-256 of data.
func sha256Hex(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// writeFile creates a file at dir/rel with the given content, creating parent
// directories as needed.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// gitRun executes a git command in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// --- tests ---

func TestPackDir_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	res, err := PackDir(dir, &buf, defaultOpts())
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	if res.EntryCount != 0 {
		t.Errorf("EntryCount = %d, want 0", res.EntryCount)
	}
	if res.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0 (zstd frame overhead)", res.SizeBytes)
	}
	if len(res.SHA256) != 64 {
		t.Errorf("SHA256 length = %d, want 64", len(res.SHA256))
	}

	entries := extractTarZst(t, buf.Bytes())
	if len(entries) != 0 {
		t.Errorf("extracted %d entries from empty dir, want 0", len(entries))
	}
}

func TestPackDir_BasicFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hello.txt", "hello world")
	writeFile(t, dir, "sub/deep.txt", "nested content")

	var buf bytes.Buffer
	res, err := PackDir(dir, &buf, defaultOpts())
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}
	if res.EntryCount < 2 {
		t.Errorf("EntryCount = %d, want >= 2", res.EntryCount)
	}

	entries := extractTarZst(t, buf.Bytes())
	if got, ok := entries["hello.txt"]; !ok || got != "hello world" {
		t.Errorf("hello.txt content = %q ok=%v", got, ok)
	}
	if got, ok := entries["sub/deep.txt"]; !ok || got != "nested content" {
		t.Errorf("sub/deep.txt content = %q ok=%v", got, ok)
	}
}

func TestPackDir_RespectsDockerIgnore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.go", "package main")
	writeFile(t, dir, "secret.env", "SECRET=hunter2")
	writeFile(t, dir, ".dockerignore", "secret.env\n")

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	if _, ok := entries["secret.env"]; ok {
		t.Error("secret.env should be excluded by .dockerignore")
	}
	if _, ok := entries["app.go"]; !ok {
		t.Error("app.go should be included")
	}
}

func TestPackDir_FallbackIgnoreNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.js", "console.log('hi')")
	writeFile(t, dir, "node_modules/lib/foo.js", "// dep")

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	if _, ok := entries["node_modules/lib/foo.js"]; ok {
		t.Error("node_modules should be excluded by built-in fallback list")
	}
	if _, ok := entries["index.js"]; !ok {
		t.Error("index.js should be included")
	}
}

func TestPackDir_GitLsFilesIfGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	writeFile(t, dir, "tracked.go", "package main")
	writeFile(t, dir, "ignored.log", "log output")
	writeFile(t, dir, ".gitignore", "*.log\n")
	gitRun(t, dir, "add", "tracked.go", ".gitignore")
	gitRun(t, dir, "commit", "-m", "init")

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	if _, ok := entries["tracked.go"]; !ok {
		t.Error("tracked.go should be included")
	}
	if _, ok := entries["ignored.log"]; ok {
		t.Error("ignored.log should be excluded via .gitignore")
	}
}

func TestPackDir_RejectsOversize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "big.bin", strings.Repeat("x", 1024))

	var buf bytes.Buffer
	_, err := PackDir(dir, &buf, PackOptions{MaxBytes: 10, MaxEntries: math.MaxInt})
	if err == nil {
		t.Fatal("expected ErrTarballTooLarge, got nil")
	}
	if !strings.Contains(err.Error(), "tarball exceeds MaxBytes") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPackDir_RejectsTooManyEntries(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		writeFile(t, dir, strings.Repeat(string(rune('a'+i)), 1)+"file.txt", "data")
	}

	var buf bytes.Buffer
	_, err := PackDir(dir, &buf, PackOptions{MaxBytes: math.MaxInt64, MaxEntries: 2})
	if err == nil {
		t.Fatal("expected ErrTooManyEntries, got nil")
	}
	if !strings.Contains(err.Error(), "file count exceeds MaxEntries") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPackDir_SymlinkRecorded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.txt", "symlink target content")
	if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	if got, ok := entries["link.txt"]; !ok {
		t.Error("link.txt symlink not found in archive")
	} else if got != "-> target.txt" {
		t.Errorf("link.txt linkname = %q, want %q", got, "-> target.txt")
	}
}

func TestPackDir_SymlinkBrokenIncluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "real")
	// Broken symlink: target does not exist.
	if err := os.Symlink("nonexistent.txt", filepath.Join(dir, "broken.txt")); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	var buf bytes.Buffer
	// PackDir must succeed. os.Readlink works for broken symlinks (it reads the
	// stored linkname without resolving); the symlink entry is included in the
	// tar and T6 validates the target on extraction.
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir returned error for broken symlink: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	if _, ok := entries["real.txt"]; !ok {
		t.Error("real.txt should be packed")
	}
	if _, ok := entries["broken.txt"]; !ok {
		t.Errorf("expected broken.txt to be present in archive (server T6 rejects unsafe targets, not packer)")
	}
}

func TestPackDir_FileModeMasked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exec.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh"), 0777); err != nil { //nolint:gosec
		t.Fatalf("WriteFile: %v", err)
	}

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	zr, err := zstd.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if hdr.Name == "exec.sh" {
			if hdr.Mode != 0644 {
				t.Errorf("exec.sh mode = %04o, want 0644", hdr.Mode)
			}
			return
		}
	}
	t.Error("exec.sh not found in archive")
}

func TestPackDir_PathInjectionRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	writeFile(t, dir, "safe.go", "package main")
	gitRun(t, dir, "add", "safe.go")
	gitRun(t, dir, "commit", "-m", "init")

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	for name := range entries {
		if strings.HasPrefix(name, "..") || strings.Contains(name, "/../") {
			t.Errorf("path injection found in archive entry: %q", name)
		}
	}
}

func TestPackDir_NestedDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c/deep.txt", "deeply nested")

	var buf bytes.Buffer
	if _, err := PackDir(dir, &buf, defaultOpts()); err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	entries := extractTarZst(t, buf.Bytes())
	if got, ok := entries["a/b/c/deep.txt"]; !ok || got != "deeply nested" {
		t.Errorf("a/b/c/deep.txt content = %q ok=%v", got, ok)
	}
}

func TestPackDir_InvalidPath(t *testing.T) {
	var buf bytes.Buffer
	if _, err := PackDir("", &buf, defaultOpts()); err == nil {
		t.Error("expected error for empty rootPath")
	}
	if _, err := PackDir("/nonexistent/path/xyz_doesnotexist", &buf, defaultOpts()); err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestPackDir_SHAAndSizeConsistent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "data.txt", "consistent content")

	var buf bytes.Buffer
	res, err := PackDir(dir, &buf, defaultOpts())
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	if res.SizeBytes != int64(buf.Len()) {
		t.Errorf("SizeBytes = %d, actual buf len = %d", res.SizeBytes, buf.Len())
	}

	want := sha256Hex(buf.Bytes())
	if res.SHA256 != want {
		t.Errorf("SHA256 mismatch: got %q, want %q", res.SHA256, want)
	}
}

func TestPackDir_CapAbortsMidFile(t *testing.T) {
	dir := t.TempDir()
	// Write 10 KB of varied (incompressible) data so the compressed output
	// exceeds the cap even after zstd compression.
	content := make([]byte, 10*1024)
	for i := range content {
		content[i] = byte(i & 0xFF)
	}
	abs := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(abs, content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Cap at 100 bytes: above empty-archive overhead (~16 B) but well below
	// what the tar header + file content compresses to (~400+ B).
	var buf bytes.Buffer
	_, err := PackDir(dir, &buf, PackOptions{MaxBytes: 100, MaxEntries: math.MaxInt})
	if !errors.Is(err, ErrTarballTooLarge) {
		t.Fatalf("expected ErrTarballTooLarge, got %v", err)
	}
	// buf must not exceed cap + small headroom for buffered writes.
	if int64(buf.Len()) > 200 {
		t.Fatalf("output exceeded cap+headroom: %d bytes", buf.Len())
	}
}

func TestPackDir_EnumerationPathsConsistent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	// Non-git directory.
	nonGit := t.TempDir()
	writeFile(t, nonGit, "a.txt", "hello")
	writeFile(t, nonGit, "nested/b.txt", "world")

	// Git directory with identical content.
	gitDir := t.TempDir()
	writeFile(t, gitDir, "a.txt", "hello")
	writeFile(t, gitDir, "nested/b.txt", "world")
	gitRun(t, gitDir, "init", "-q")
	gitRun(t, gitDir, "add", ".")
	gitRun(t, gitDir, "-c", "user.email=t@e.com", "-c", "user.name=t", "commit", "-qm", "init")

	var bufA, bufB bytes.Buffer
	resA, errA := PackDir(nonGit, &bufA, defaultOpts())
	resB, errB := PackDir(gitDir, &bufB, defaultOpts())
	if errA != nil || errB != nil {
		t.Fatalf("pack errs: %v / %v", errA, errB)
	}
	if resA.EntryCount != resB.EntryCount {
		t.Errorf("entry counts differ: walk=%d, git=%d", resA.EntryCount, resB.EntryCount)
	}
}
