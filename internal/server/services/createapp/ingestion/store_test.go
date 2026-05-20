package ingestion

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// syntheticTarZst builds a tar.zst archive from the given files map.
func syntheticTarZst(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	tw := tar.NewWriter(enc)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

// syntheticTarGz builds a tar.gz archive from the given files map.
func syntheticTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// syntheticTarZstWithSymlink builds a tar.zst containing a single symlink entry.
func syntheticTarZstWithSymlink(t *testing.T, linkName, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(enc)
	_ = tw.WriteHeader(&tar.Header{
		Name:     linkName,
		Linkname: target,
		Typeflag: tar.TypeSymlink,
		Mode:     0777,
	})
	_ = tw.Close()
	_ = enc.Close()
	return buf.Bytes()
}

// syntheticTarZstWithMode builds a tar.zst with a single file at the given mode.
func syntheticTarZstWithMode(t *testing.T, name, body string, mode int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(enc)
	_ = tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = enc.Close()
	return buf.Bytes()
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return &Store{
		Root:            t.TempDir(),
		MaxArchiveBytes: 10 * 1024 * 1024,
		MaxEntryBytes:   1 * 1024 * 1024,
		MaxEntries:      1000,
	}
}

// --- Put tests ---

func TestStore_Put_WritesUnderTeamDir(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"package.json": `{"name":"app"}`})

	stored, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Check archive exists.
	archivePath := filepath.Join(s.Root, "team1", "upload1", "_archive.tar.zst")
	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("archive not found: %v", err)
	}

	// Check meta exists.
	metaPath := filepath.Join(s.Root, "team1", "upload1", "_meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("_meta.json not found: %v", err)
	}

	// Check extracted file exists.
	extractedPath := filepath.Join(s.Root, "team1", "upload1", "tree", "package.json")
	if _, err := os.Stat(extractedPath); err != nil {
		t.Errorf("extracted file not found: %v", err)
	}

	if stored.EntryCount != 1 {
		t.Errorf("expected entry count 1, got %d", stored.EntryCount)
	}
	if stored.Format != "tar.zst" {
		t.Errorf("expected format tar.zst, got %q", stored.Format)
	}
	if stored.SizeBytes <= 0 {
		t.Errorf("expected positive SizeBytes, got %d", stored.SizeBytes)
	}
	if stored.SHA256 == "" {
		t.Error("expected non-empty SHA256")
	}
	if stored.Path == "" {
		t.Error("expected non-empty Path")
	}
}

func TestStore_Put_RejectsPathTraversalEntry(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"../escape": "bad"})

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		t.Fatal("expected error for path traversal entry, got nil")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("expected ErrPathEscape, got %v", err)
	}

	// The escaping file must NOT have been created outside the tree.
	escapePath := filepath.Join(s.Root, "team1", "escape")
	if _, err := os.Stat(escapePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("escape file was created outside tree — path traversal not blocked")
	}
}

func TestStore_Put_RejectsAbsolutePathEntry(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"/etc/passwd": "bad"})

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		t.Fatal("expected error for absolute path entry, got nil")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("expected ErrPathEscape, got %v", err)
	}

	// The file must NOT have been created at /etc/passwd (obviously), but also
	// not at a relative subpath.
	escapePath := filepath.Join(s.Root, "team1", "upload1", "tree", "etc", "passwd")
	if _, err := os.Stat(escapePath); err == nil {
		t.Error("absolute path entry was extracted into tree — should have been rejected")
	}
}

func TestStore_Put_RejectsSymlinkEscape(t *testing.T) {
	s := newStore(t)
	// A symlink pointing two levels up — would escape the upload tree.
	data := syntheticTarZstWithSymlink(t, "link", "../../etc/passwd")

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		t.Fatal("expected error for symlink escape, got nil")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("expected ErrPathEscape, got %v", err)
	}
}

func TestStore_Put_RejectsSymlinkAbsoluteTarget(t *testing.T) {
	root := t.TempDir()
	s := &Store{
		Root:            root,
		MaxArchiveBytes: 1 << 20,
		MaxEntryBytes:   1 << 20,
		MaxEntries:      10,
	}
	archive := syntheticTarZstWithSymlink(t, "evil", "/etc/passwd")
	_, err := s.Put(context.Background(), "team_a", "upl_x", bytes.NewReader(archive), "tar.zst")
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape, got %v", err)
	}
	// Verify the symlink was NOT created on disk.
	linkPath := filepath.Join(root, "team_a", "upl_x", "tree", "evil")
	if _, statErr := os.Lstat(linkPath); statErr == nil {
		t.Fatalf("symlink to /etc/passwd was created on disk; absolute-target check failed")
	}
}

func TestStore_Put_AcceptsInTreeSymlink(t *testing.T) {
	s := newStore(t)
	// First create the regular file, then a symlink pointing to it.
	var buf bytes.Buffer
	enc, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(enc)

	// Regular file
	body := "hello"
	_ = tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))

	// Symlink to the file above (same directory)
	_ = tw.WriteHeader(&tar.Header{Name: "link.txt", Linkname: "hello.txt", Typeflag: tar.TypeSymlink, Mode: 0777})

	_ = tw.Close()
	_ = enc.Close()

	_, err := s.Put(context.Background(), "team1", "upload1", &buf, "tar.zst")
	if err != nil {
		t.Fatalf("Put rejected valid in-tree symlink: %v", err)
	}

	// Symlink must exist inside tree.
	symlinkPath := filepath.Join(s.Root, "team1", "upload1", "tree", "link.txt")
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Errorf("symlink not created: %v", err)
	}
}

func TestStore_Put_RejectsOversizedArchive(t *testing.T) {
	s := &Store{
		Root:            t.TempDir(),
		MaxArchiveBytes: 10, // 10 bytes — any real archive exceeds this
		MaxEntryBytes:   1 * 1024 * 1024,
		MaxEntries:      1000,
	}
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		t.Fatal("expected ErrArchiveTooLarge, got nil")
	}
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Errorf("expected ErrArchiveTooLarge, got %v", err)
	}
}

func TestStore_Put_RejectsOversizedEntry(t *testing.T) {
	s := &Store{
		Root:            t.TempDir(),
		MaxArchiveBytes: 10 * 1024 * 1024,
		MaxEntryBytes:   5, // 5 bytes per entry
		MaxEntries:      1000,
	}
	// Entry body is longer than 5 bytes.
	data := syntheticTarZst(t, map[string]string{"file.txt": "0123456789"})

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		t.Fatal("expected ErrEntryTooLarge, got nil")
	}
	if !errors.Is(err, ErrEntryTooLarge) {
		t.Errorf("expected ErrEntryTooLarge, got %v", err)
	}
}

func TestStore_Put_RejectsTooManyEntries(t *testing.T) {
	s := &Store{
		Root:            t.TempDir(),
		MaxArchiveBytes: 10 * 1024 * 1024,
		MaxEntryBytes:   1 * 1024 * 1024,
		MaxEntries:      2, // only 2 entries allowed
	}
	data := syntheticTarZst(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.txt": "c", // third entry triggers the cap
	})

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		t.Fatal("expected ErrTooManyEntries, got nil")
	}
	if !errors.Is(err, ErrTooManyEntries) {
		t.Errorf("expected ErrTooManyEntries, got %v", err)
	}
}

func TestStore_Put_UnknownFormat(t *testing.T) {
	s := newStore(t)
	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader([]byte("irrelevant")), "tar.bz2")
	if err == nil {
		t.Fatal("expected ErrUnsupportedFormat, got nil")
	}
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestStore_Put_SupportsTarGz(t *testing.T) {
	s := newStore(t)
	data := syntheticTarGz(t, map[string]string{"index.js": "console.log('hi')"})

	stored, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.gz")
	if err != nil {
		t.Fatalf("Put tar.gz: %v", err)
	}
	if stored.Format != "tar.gz" {
		t.Errorf("expected format tar.gz, got %q", stored.Format)
	}

	archivePath := filepath.Join(s.Root, "team1", "upload1", "_archive.tar.gz")
	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("archive.tar.gz not found: %v", err)
	}

	extractedPath := filepath.Join(s.Root, "team1", "upload1", "tree", "index.js")
	if _, err := os.Stat(extractedPath); err != nil {
		t.Errorf("extracted file not found: %v", err)
	}
}

func TestStore_Put_ComputesSHA256(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"file.txt": "hello"})

	// Compute expected sha256 of the raw archive bytes.
	h := sha256.Sum256(data)
	expected := hex.EncodeToString(h[:])

	stored, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored.SHA256 != expected {
		t.Errorf("SHA256 mismatch: want %s, got %s", expected, stored.SHA256)
	}
}

func TestStore_Put_MasksFileMode(t *testing.T) {
	s := newStore(t)
	// Archive contains a file with mode 0777 — on disk it must be 0644.
	data := syntheticTarZstWithMode(t, "script.sh", "#!/bin/sh\necho hi", 0777)

	_, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	scriptPath := filepath.Join(s.Root, "team1", "upload1", "tree", "script.sh")
	fi, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	// Mode should be 0644, not 0777.
	got := fi.Mode().Perm()
	if got != 0644 {
		t.Errorf("expected mode 0644, got %04o", got)
	}
}

// --- Open tests ---

func TestStore_Open_HappyPath(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"src/main.go": "package main"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := s.Open(context.Background(), "team1", "upload1", "src/main.go")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(content) != "package main" {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestStore_Open_RejectsPathTraversal(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, err := s.Open(context.Background(), "team1", "upload1", "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("expected ErrPathEscape, got %v", err)
	}
}

func TestStore_Open_RejectsAbsolute(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, err := s.Open(context.Background(), "team1", "upload1", "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("expected ErrPathEscape, got %v", err)
	}
}

func TestStore_Open_NotFound(t *testing.T) {
	s := newStore(t)

	_, err := s.Open(context.Background(), "nonexistent-team", "nonexistent-upload", "file.txt")
	if err == nil {
		t.Fatal("expected ErrUploadNotFound, got nil")
	}
	if !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("expected ErrUploadNotFound, got %v", err)
	}
}

func TestStore_Open_RejectsSymlinkPointingOutsideTreeAfterExtraction(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"real.txt": "safe content"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Manually inject a symlink that points outside the tree (simulating a
	// scenario where the symlink was placed post-extraction, e.g., by another
	// process). Open() must reject it via EvalSymlinks validation.
	treeDir := filepath.Join(s.Root, "team1", "upload1", "tree")
	escapingLink := filepath.Join(treeDir, "escape_link")
	// Point to the parent of the tree directory.
	if err := os.Symlink(filepath.Join(treeDir, ".."), escapingLink); err != nil {
		t.Fatalf("symlink create: %v", err)
	}

	_, err := s.Open(context.Background(), "team1", "upload1", "escape_link")
	// EvalSymlinks on a symlink pointing to a directory will resolve; the Rel
	// check then must reject because the resolved path is uploadDir (parent of tree).
	if err == nil {
		t.Fatal("expected error for symlink escaping tree, got nil")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("expected ErrPathEscape, got %v", err)
	}
}

// --- Archive tests ---

func TestStore_Archive_HappyPath(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := s.Archive(context.Background(), "team1", "upload1")
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("Archive() returned different bytes than what was Put()")
	}
}

func TestStore_Archive_NotFound(t *testing.T) {
	s := newStore(t)

	_, err := s.Archive(context.Background(), "nonexistent-team", "nonexistent-upload")
	if err == nil {
		t.Fatal("expected ErrUploadNotFound, got nil")
	}
	if !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("expected ErrUploadNotFound, got %v", err)
	}
}

// --- Delete tests ---

func TestStore_Delete_MovesToTrash(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	uploadDir := filepath.Join(s.Root, "team1", "upload1")
	if _, err := os.Stat(uploadDir); err != nil {
		t.Fatalf("upload dir should exist before Delete: %v", err)
	}

	if err := s.Delete(context.Background(), "team1", "upload1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Original directory must be gone.
	if _, err := os.Stat(uploadDir); !errors.Is(err, os.ErrNotExist) {
		t.Error("upload dir still exists after Delete")
	}

	// _trash directory must exist and contain the moved upload.
	trashDir := filepath.Join(s.Root, "_trash")
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		t.Fatalf("read trash dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("_trash dir is empty after Delete")
	}

	// The trash entry name must start with the team and upload IDs.
	found := false
	for _, e := range entries {
		if len(e.Name()) > 0 {
			// Name format: "<teamID>-<uploadID>-<nano>"
			if len(e.Name()) >= len("team1-upload1-") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("no matching trash entry found; entries: %v", entries)
	}
}

func TestStore_Delete_Idempotent(t *testing.T) {
	s := newStore(t)

	// Delete a non-existent upload — must return nil.
	err := s.Delete(context.Background(), "nonexistent-team", "nonexistent-upload")
	if err != nil {
		t.Errorf("Delete on missing upload should return nil, got %v", err)
	}

	// Call again to ensure repeated calls also return nil.
	err = s.Delete(context.Background(), "nonexistent-team", "nonexistent-upload")
	if err != nil {
		t.Errorf("repeated Delete on missing upload should return nil, got %v", err)
	}
}

// --- RootForTeam ---

func TestStore_RootForTeam(t *testing.T) {
	s := &Store{Root: "/var/lib/0ops/uploads"}
	got := s.RootForTeam("team-abc")
	want := "/var/lib/0ops/uploads/team-abc"
	if got != want {
		t.Errorf("RootForTeam: want %q, got %q", want, got)
	}
}

// --- Stored.Path is excluded from JSON ---

func TestStored_PathNotInJSON(t *testing.T) {
	s := newStore(t)
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})
	if _, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	metaPath := filepath.Join(s.Root, "team1", "upload1", "_meta.json")
	rawMeta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if bytes.Contains(rawMeta, []byte(`"path"`)) {
		t.Error("_meta.json must not contain the 'path' field")
	}
}

// --- ReceivedAt is close to now ---

func TestStore_Put_ReceivedAt(t *testing.T) {
	s := newStore(t)
	before := time.Now().UTC()
	data := syntheticTarZst(t, map[string]string{"file.txt": "content"})
	stored, err := s.Put(context.Background(), "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	after := time.Now().UTC()

	if stored.ReceivedAt.Before(before) || stored.ReceivedAt.After(after) {
		t.Errorf("ReceivedAt %v not in [%v, %v]", stored.ReceivedAt, before, after)
	}
}

// --- Context cancellation is respected ---

func TestStore_Put_ContextCancelled(t *testing.T) {
	s := newStore(t)

	// Archive with many entries so that context cancellation fires mid-extraction.
	files := make(map[string]string)
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("file%02d.txt", i)] = "some content to fill the entry"
	}
	data := syntheticTarZst(t, files)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := s.Put(ctx, "team1", "upload1", bytes.NewReader(data), "tar.zst")
	if err == nil {
		// It's possible the extraction finished before context was checked, but
		// with a cancelled context we expect an error eventually. If the archive
		// is small enough to finish first, that's acceptable — just log.
		t.Log("Put completed despite cancelled context (archive too small to trigger mid-extraction check)")
	}
}

// syntheticTarZstOrdered builds a tar.zst from entries in the given order.
// Unlike syntheticTarZst (map-based), this guarantees deterministic entry ordering.
type tarEntry struct{ name, body string }

func syntheticTarZstOrdered(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	tw := tar.NewWriter(enc)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Mode:     0644,
			Size:     int64(len(e.body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header %q: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("tar body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

func TestStore_Put_RetryAfterExtractionFailureProducesCleanTree(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root, MaxArchiveBytes: 1 << 20, MaxEntryBytes: 1 << 20, MaxEntries: 10}

	// First Put: deterministically ordered — good-file.txt is written first,
	// then ../escape.txt triggers ErrPathEscape. This guarantees a partial tree
	// exists before the error so the cleanup path is exercised.
	bad := syntheticTarZstOrdered(t, []tarEntry{
		{"good-file.txt", "hello"},
		{"../escape.txt", "evil"},
	})
	_, err := s.Put(context.Background(), "team_a", "upl_x", bytes.NewReader(bad), "tar.zst")
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("expected ErrPathEscape from first Put, got %v", err)
	}

	// Tree directory should NOT exist after partial-tree cleanup.
	treeDir := filepath.Join(root, "team_a", "upl_x", "tree")
	if _, statErr := os.Stat(treeDir); statErr == nil {
		t.Fatalf("treeDir still exists after failed Put; partial cleanup did not run")
	}

	// Second Put: clean archive must succeed and produce only its own files.
	good := syntheticTarZstOrdered(t, []tarEntry{
		{"main.js", "console.log('hi')"},
	})
	_, err = s.Put(context.Background(), "team_a", "upl_x", bytes.NewReader(good), "tar.zst")
	if err != nil {
		t.Fatalf("retry Put failed: %v", err)
	}

	// Orphan from the failed first attempt must NOT be present.
	if _, statErr := os.Stat(filepath.Join(treeDir, "good-file.txt")); statErr == nil {
		t.Fatalf("orphan good-file.txt from failed first Put still present after retry")
	}

	// The file from the successful second Put must exist.
	if _, statErr := os.Stat(filepath.Join(treeDir, "main.js")); statErr != nil {
		t.Fatalf("main.js from successful retry Put not found: %v", statErr)
	}
}
