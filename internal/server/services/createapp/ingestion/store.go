// Package ingestion owns the on-disk ingest tree for per-team upload payloads.
//
// Layout under Root:
//
//	<Root>/
//	  <team_id>/
//	    <upload_id>/
//	      _archive.<format>     (original compressed archive)
//	      _meta.json            (receipt JSON)
//	      tree/                 (extracted files)
//	        package.json
//	        ...
//	  _trash/                   (deleted uploads pending GC)
//
// Path safety invariants (ADR-0013 §9):
//   - Store NEVER accepts raw paths from request inputs; paths are constructed
//     from (teamID, uploadID, relPath) tuples that go through Clean + Rel-prefix
//     validation.
//   - Symlink entries inside an archive must resolve to a target inside the
//     same upload tree, or the entry is rejected.
//   - File permissions are masked to 0644, directory permissions to 0755.
//     Setuid / setgid / world-writable bits are stripped.
package ingestion

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Store owns the on-disk ingest tree rooted at Root. Every team gets a
// sub-directory; every upload gets a further sub-directory.
//
// Path safety invariants (ADR-0013 §9):
//   - Store NEVER accepts raw paths from request inputs; paths are constructed
//     from (teamID, uploadID, relPath) tuples that go through Clean + Rel-prefix
//     validation.
//   - Symlink entries inside an archive must resolve to a target inside the
//     same upload tree, or the entry is rejected.
//   - File permissions are masked to 0644, directory permissions to 0755.
//     Setuid / setgid / world-writable bits are stripped.
type Store struct {
	Root            string // absolute path; e.g. /var/lib/0ops/uploads
	MaxArchiveBytes int64  // hard cap on the .tar.zst / .tar.gz blob (0 = no cap)
	MaxEntryBytes   int64  // hard cap on each individual tar entry (0 = no cap)
	MaxEntries      int    // hard cap on number of tar entries (0 = no cap)
}

// Stored carries the receipt of a successful Put().
type Stored struct {
	SHA256     string    `json:"sha256"`
	SizeBytes  int64     `json:"size_bytes"`
	EntryCount int       `json:"entry_count"`
	Format     string    `json:"format"`
	ReceivedAt time.Time `json:"received_at"`

	// Path is not serialised to _meta.json — it is local to this server instance.
	Path string `json:"-"`
}

// ErrPathEscape signals an attempt to access a path outside the
// (Root, teamID, uploadID, "tree") sandbox. Returned by Open() and during
// extraction in Put() when an entry's resolved path would land outside.
var ErrPathEscape = errors.New("path escape: relative path resolves outside upload tree")

// ErrUploadNotFound is returned by Open() / Archive() when the upload
// directory does not exist for the given team. Distinct from db.ErrUploadNotFound
// in that this layer never sees the DB; it's a pure filesystem-not-found.
var ErrUploadNotFound = errors.New("upload not found on disk")

// ErrUnsupportedFormat signals a Put() with an unrecognised archive format
// string. Allowed: "tar.zst", "tar.gz".
var ErrUnsupportedFormat = errors.New("unsupported archive format")

// ErrArchiveTooLarge is returned during Put() when the archive stream exceeds MaxArchiveBytes.
var ErrArchiveTooLarge = errors.New("archive exceeds MaxArchiveBytes")

// ErrEntryTooLarge is returned during Put() when a single tar entry exceeds MaxEntryBytes.
var ErrEntryTooLarge = errors.New("entry exceeds MaxEntryBytes")

// ErrTooManyEntries is returned during Put() when the entry count exceeds MaxEntries.
var ErrTooManyEntries = errors.New("entry count exceeds MaxEntries")

// Put writes a fresh archive to <Root>/<teamID>/<uploadID>/_archive.<format>
// and extracts under <Root>/<teamID>/<uploadID>/tree/. Computes sha256 while
// streaming the archive to disk. All path entries are sanitized.
//
// The archive file is written to a .tmp path first, then renamed atomically
// after successful extraction.
func (s *Store) Put(ctx context.Context, teamID, uploadID string, r io.Reader, format string) (Stored, error) {
	if format != "tar.zst" && format != "tar.gz" {
		return Stored{}, ErrUnsupportedFormat
	}

	uploadDir := filepath.Join(s.Root, teamID, uploadID)
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return Stored{}, fmt.Errorf("create upload dir: %w", err)
	}

	treeDir := filepath.Join(uploadDir, "tree")
	if err := os.MkdirAll(treeDir, 0755); err != nil {
		return Stored{}, fmt.Errorf("create tree dir: %w", err)
	}

	archiveFinal := filepath.Join(uploadDir, "_archive."+format)

	archiveFile, err := os.CreateTemp(uploadDir, "_archive."+format+".tmp.*")
	if err != nil {
		return Stored{}, fmt.Errorf("create archive tmp: %w", err)
	}
	archiveTmp := archiveFile.Name()

	// Clean up the tmp file on failure; ignore close errors on happy path.
	tmpRemoved := false
	defer func() {
		if !tmpRemoved {
			_ = os.Remove(archiveTmp)
		}
	}()

	// Cap the archive size while streaming if MaxArchiveBytes is set.
	var archiveReader io.Reader = r
	if s.MaxArchiveBytes > 0 {
		archiveReader = &limitedReader{r: r, n: s.MaxArchiveBytes, err: ErrArchiveTooLarge}
	}

	// TeeReader: every byte written to archiveFile also passes through sha256.
	hasher := sha256.New()
	tee := io.TeeReader(archiveReader, hasher)

	written, err := io.Copy(archiveFile, tee)
	if err != nil {
		_ = archiveFile.Close()
		return Stored{}, fmt.Errorf("write archive: %w", err)
	}
	if err := archiveFile.Close(); err != nil {
		return Stored{}, fmt.Errorf("close archive tmp: %w", err)
	}

	archiveSum := hex.EncodeToString(hasher.Sum(nil))

	// Re-open the archive for extraction.
	af, err := os.Open(archiveTmp)
	if err != nil {
		return Stored{}, fmt.Errorf("reopen archive: %w", err)
	}
	defer af.Close()

	dec, cleanup, err := openDecompressor(af, format)
	if err != nil {
		return Stored{}, fmt.Errorf("open decompressor: %w", err)
	}
	defer cleanup()

	entryCount, err := s.extractTar(ctx, dec, treeDir)
	if err != nil {
		// Wipe partial tree so retry starts clean.
		_ = os.RemoveAll(treeDir)
		return Stored{}, err
	}

	// Atomic rename of archive after successful extraction.
	if err := os.Rename(archiveTmp, archiveFinal); err != nil {
		return Stored{}, fmt.Errorf("rename archive: %w", err)
	}
	tmpRemoved = true

	stored := Stored{
		Path:       uploadDir,
		SHA256:     archiveSum,
		SizeBytes:  written,
		EntryCount: entryCount,
		Format:     format,
		ReceivedAt: time.Now().UTC(),
	}

	if err := s.writeMeta(uploadDir, stored); err != nil {
		// Non-fatal: metadata write failure should not abort the upload.
		// The archive and tree are already written.
		slog.Warn("ingestion: writeMeta failed",
			"team", teamID,
			"upload", uploadID,
			"err", err)
	}

	return stored, nil
}

// Open returns a ReadCloser for a specific file inside <Root>/<teamID>/<uploadID>/tree/<relPath>.
// relPath is sanitized via Clean + Rel-prefix; absolute paths, ".." prefixes,
// and symlinks resolving outside the tree are rejected with ErrPathEscape.
func (s *Store) Open(ctx context.Context, teamID, uploadID, relPath string) (io.ReadCloser, error) {
	treeDir := filepath.Join(s.Root, teamID, uploadID, "tree")

	// Verify the upload exists.
	if _, err := os.Stat(treeDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUploadNotFound
		}
		return nil, fmt.Errorf("stat tree dir: %w", err)
	}

	safe, err := sanitizeRelPath(relPath)
	if err != nil {
		return nil, err
	}

	candidate := filepath.Join(treeDir, safe)

	// EvalSymlinks defense: resolve the final path and confirm it stays inside the tree.
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUploadNotFound
		}
		return nil, fmt.Errorf("eval symlinks: %w", err)
	}

	resolvedTree, err := filepath.EvalSymlinks(treeDir)
	if err != nil {
		return nil, fmt.Errorf("eval symlinks for tree: %w", err)
	}

	rel, err := filepath.Rel(resolvedTree, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, ErrPathEscape
	}

	f, err := os.Open(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUploadNotFound
		}
		return nil, fmt.Errorf("open file: %w", err)
	}
	return f, nil
}

// Archive returns a ReadCloser for the original (compressed) archive bytes.
// Returns ErrUploadNotFound if the directory has been GC'd or never existed.
func (s *Store) Archive(ctx context.Context, teamID, uploadID string) (io.ReadCloser, error) {
	uploadDir := filepath.Join(s.Root, teamID, uploadID)

	// Try each supported format.
	for _, format := range []string{"tar.zst", "tar.gz"} {
		archivePath := filepath.Join(uploadDir, "_archive."+format)
		f, err := os.Open(archivePath)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("open archive: %w", err)
		}
	}

	return nil, ErrUploadNotFound
}

// Delete moves <Root>/<teamID>/<uploadID>/ into <Root>/_trash/<teamID>-<uploadID>-<unix-nano>/.
// The actual unlink happens later (or via cron) to avoid races with concurrent
// Open() calls. Returns nil if the directory is already gone (idempotent).
func (s *Store) Delete(ctx context.Context, teamID, uploadID string) error {
	src := filepath.Join(s.Root, teamID, uploadID)

	trashDir := filepath.Join(s.Root, "_trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		return fmt.Errorf("create trash dir: %w", err)
	}

	trashName := fmt.Sprintf("%s-%s-%d", teamID, uploadID, time.Now().UnixNano())
	target := filepath.Join(trashDir, trashName)

	if err := os.Rename(src, target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // idempotent
		}
		return fmt.Errorf("move to trash: %w", err)
	}
	return nil
}

// RootForTeam returns the absolute path to a team's sub-directory under Root.
// Used by inspector / GC for listing. Caller must NOT use this path for
// extraction sinks — only for read-only enumeration.
func (s *Store) RootForTeam(teamID string) string {
	return filepath.Join(s.Root, teamID)
}

// --- private helpers ---

// sanitizeRelPath cleans a caller-supplied relative path, rejecting any
// component that would escape the sandbox (absolute paths, any ".." component).
//
// Security rationale: we explicitly reject any path that contains ".." as a
// path component BEFORE applying filepath.Clean. The anchor-to-root trick
// (filepath.Clean("/" + rel)) silently collapses "../escape" to "/escape",
// which would allow the entry to be written to a different (but still "safe")
// location. That is misleading and may mask injection attempts; we want a hard
// rejection instead.
func sanitizeRelPath(rel string) (string, error) {
	// Reject absolute paths.
	if filepath.IsAbs(rel) {
		return "", ErrPathEscape
	}

	// Normalize OS-specific separators and clean the path.
	rel = filepath.FromSlash(rel)

	// Reject any path component that is exactly "..". This covers:
	//   "../escape", "a/../../b", "foo/../../../etc/passwd", etc.
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".." {
			return "", ErrPathEscape
		}
	}

	clean := filepath.Clean(rel)
	if clean == "." || clean == "" {
		return "", ErrPathEscape
	}
	// Final sanity: cleaned result must not start with ".."
	if strings.HasPrefix(clean, "..") {
		return "", ErrPathEscape
	}
	return clean, nil
}

// openDecompressor returns a decompressing reader plus a close function for
// the given format. Caller must call the close function when done.
func openDecompressor(r io.Reader, format string) (io.Reader, func(), error) {
	switch format {
	case "tar.zst":
		dec, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("zstd reader: %w", err)
		}
		return dec, dec.Close, nil
	case "tar.gz":
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip reader: %w", err)
		}
		return gz, func() { _ = gz.Close() }, nil
	default:
		return nil, nil, ErrUnsupportedFormat
	}
}

// extractTar reads tar entries from dec and writes them under treeDir.
// Returns the number of entries processed (excluding skipped special entries).
func (s *Store) extractTar(ctx context.Context, dec io.Reader, treeDir string) (int, error) {
	tr := tar.NewReader(dec)
	count := 0

	for {
		if err := ctx.Err(); err != nil {
			return count, err
		}

		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return count, fmt.Errorf("read tar header: %w", err)
		}

		if s.MaxEntries > 0 && count >= s.MaxEntries {
			return count, ErrTooManyEntries
		}

		// Sanitize the entry name.
		safe, err := sanitizeRelPath(hdr.Name)
		if err != nil {
			// Reject path-escaping entries.
			return count, fmt.Errorf("tar entry %q: %w", hdr.Name, ErrPathEscape)
		}

		target := filepath.Join(treeDir, safe)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return count, fmt.Errorf("mkdir %q: %w", safe, err)
			}
			count++

		case tar.TypeReg, tar.TypeRegA:
			if s.MaxEntryBytes > 0 && hdr.Size > s.MaxEntryBytes {
				return count, ErrEntryTooLarge
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return count, fmt.Errorf("mkdir parent %q: %w", safe, err)
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				return count, fmt.Errorf("create file %q: %w", safe, err)
			}
			// Enforce per-entry size cap via LimitedReader (read one extra byte to
			// detect oversize without consuming too much memory).
			var copyErr error
			if s.MaxEntryBytes > 0 {
				lr := &io.LimitedReader{R: tr, N: s.MaxEntryBytes + 1}
				n, cerr := io.Copy(f, lr)
				_ = f.Close()
				if cerr != nil {
					copyErr = cerr
				} else if n > s.MaxEntryBytes {
					copyErr = ErrEntryTooLarge
				}
			} else {
				_, cerr := io.Copy(f, tr)
				_ = f.Close()
				copyErr = cerr
			}
			if copyErr != nil {
				return count, fmt.Errorf("write file %q: %w", safe, copyErr)
			}
			count++

		case tar.TypeSymlink:
			if err := s.writeSymlink(treeDir, safe, hdr.Linkname); err != nil {
				return count, err
			}
			count++

		default:
			// Devices, FIFOs, char devices, hard links, etc. — skip silently.
			continue
		}
	}
	return count, nil
}

// writeSymlink creates a symlink at treeDir/name → linkTarget, after verifying
// that the resolved target stays within treeDir.
func (s *Store) writeSymlink(treeDir, name, linkTarget string) error {
	// Reject absolute symlink targets immediately — os.Symlink writes the target
	// verbatim, so an absolute target (e.g. "/etc/passwd") would be planted on
	// disk pointing directly outside the sandbox regardless of where filepath.Join
	// resolves the path.
	if filepath.IsAbs(linkTarget) {
		return fmt.Errorf("symlink %q → %q: %w", name, linkTarget, ErrPathEscape)
	}

	// Resolve where the symlink would point relative to its directory inside the tree.
	symlinkDir := filepath.Dir(filepath.Join(treeDir, name))
	resolved := filepath.Join(symlinkDir, linkTarget)
	resolved = filepath.Clean(resolved)

	// Ensure the resolved target stays inside treeDir.
	rel, err := filepath.Rel(treeDir, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("symlink %q → %q: %w", name, linkTarget, ErrPathEscape)
	}

	symlinkPath := filepath.Join(treeDir, name)
	if err := os.MkdirAll(filepath.Dir(symlinkPath), 0755); err != nil {
		return fmt.Errorf("mkdir for symlink %q: %w", name, err)
	}
	if err := os.Symlink(linkTarget, symlinkPath); err != nil {
		return fmt.Errorf("create symlink %q: %w", name, err)
	}
	return nil
}

// writeMeta serialises the Stored receipt (without the local Path field) to
// <uploadDir>/_meta.json. Non-fatal: caller should ignore the returned error
// unless it needs strict consistency.
func (s *Store) writeMeta(uploadDir string, stored Stored) error {
	metaPath := filepath.Join(uploadDir, "_meta.json")
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0644)
}

// limitedReader wraps an io.Reader and returns a sentinel error after n bytes.
type limitedReader struct {
	r   io.Reader
	n   int64
	err error
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, l.err
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	if err != nil {
		return n, err
	}
	if l.n <= 0 {
		return n, l.err
	}
	return n, nil
}
