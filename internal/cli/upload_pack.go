// Package cli provides the root command for the 0ops CLI.
//
// upload_pack.go implements PackDir, which produces a tar.zst archive from a
// local directory. It is consumed by T17 (upload HTTP client) and T18 (--source
// flag on apps create).
//
// # File selection strategy
//
// If rootPath/.git exists PackDir invokes `git ls-files --cached --others
// --exclude-standard` so .gitignore is honoured automatically. Submodule
// recursion (--recurse-submodules) is not performed in v1 — tracked submodule
// paths appear as a single entry; their contents are not expanded.
//
// Without .git PackDir walks the tree and applies one of:
//   - .dockerignore at rootPath (minimal parser — literal lines and simple
//     path-prefix matching; no ** globs, no negation; v1 limitation)
//   - built-in fallback list: .git, node_modules, __pycache__, .venv,
//     target, dist, build, .DS_Store
//
// # Stream layout
//
//	caller ← out
//	          ↑
//	         io.MultiWriter(out, sha256hasher, sizeCounter)
//	          ↑
//	         zstd.Writer
//	          ↑
//	         tar.Writer
//	          ↑
//	         file content via io.Copy
//
// # PackOptions zero value
//
// A zero PackOptions is unsafe: MaxBytes=0 and MaxEntries=0 disable both caps.
// Callers must set explicit limits or pass math.MaxInt64 / math.MaxInt to
// signal "no cap intentionally".
package cli

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// PackOptions configures PackDir. Zero values are unsafe — caller must set
// non-zero MaxBytes and MaxEntries to enforce caps, or pass math.MaxInt64
// and math.MaxInt to disable them.
type PackOptions struct {
	// MaxBytes caps the total tar.zst output size in bytes.
	// Exceeded → ErrTarballTooLarge. 0 = no cap.
	MaxBytes int64

	// MaxEntries caps the number of entries packed (files + dirs + symlinks,
	// excluding the implicit root). Exceeded → ErrTooManyEntries. 0 = no cap.
	MaxEntries int

	// FollowSymlinks: if true, follow symlinks during walk; if false (default),
	// symlinks are written as TypeSymlink tar entries and the server side (T6)
	// validates that the target resolves inside the tree.
	FollowSymlinks bool
}

// PackResult is the receipt of a successful PackDir call.
type PackResult struct {
	// SHA256 is the hex-encoded SHA-256 of the tar.zst bytes written to out.
	SHA256 string
	// SizeBytes is the number of tar.zst bytes written.
	SizeBytes int64
	// EntryCount is the number of entries packed (files + dirs + symlinks,
	// excluding the implicit root directory itself).
	EntryCount int
}

// Sentinels returned by PackDir.
var (
	ErrTarballTooLarge = errors.New("cli: tarball exceeds MaxBytes")
	ErrTooManyEntries  = errors.New("cli: file count exceeds MaxEntries")
	ErrPathInvalid     = errors.New("cli: invalid path")
)

// PackDir tars and zstd-compresses the contents of rootPath, writing to out.
//
// File selection:
//   - If rootPath/.git exists, uses git ls-files --cached --others
//     --exclude-standard (honours .gitignore; no submodule recursion in v1).
//   - Otherwise walks the tree with a .dockerignore filter; if no .dockerignore
//     is present falls back to a hard-coded vendor-directory skip list.
//
// File mode is masked: 0644 for regular files, 0755 for directories, 0777 for
// symlinks (tar convention; the target is written verbatim and T6 validates it).
//
// The SHA256 in PackResult is computed over the tar.zst bytes (what the server
// receives over the wire), not the inner tar stream.
func PackDir(rootPath string, out io.Writer, opt PackOptions) (PackResult, error) {
	if strings.TrimSpace(rootPath) == "" {
		return PackResult{}, ErrPathInvalid
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return PackResult{}, fmt.Errorf("%w: %s", ErrPathInvalid, err)
	}
	if !info.IsDir() {
		return PackResult{}, fmt.Errorf("%w: rootPath is not a directory", ErrPathInvalid)
	}

	entries, err := listFiles(rootPath)
	if err != nil {
		return PackResult{}, fmt.Errorf("cli: list files: %w", err)
	}

	// --- streaming pipeline ---
	hasher := sha256.New()
	sc := &sizeCounterWriter{}
	mw := io.MultiWriter(out, hasher, sc)

	zw, err := zstd.NewWriter(mw)
	if err != nil {
		return PackResult{}, fmt.Errorf("cli: create zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	entryCount := 0

	for _, rel := range entries {
		abs := filepath.Join(rootPath, rel)

		fi, err := os.Lstat(abs)
		if err != nil {
			// File disappeared between enumeration and packing — skip with warning.
			slog.Warn("cli: pack: file vanished, skipping", "path", rel, "err", err)
			continue
		}

		// Enforce MaxEntries before writing so the error is returned before any
		// partial bytes are flushed.
		if opt.MaxEntries > 0 && entryCount+1 > opt.MaxEntries {
			_ = tw.Close()
			_ = zw.Close()
			return PackResult{}, ErrTooManyEntries
		}

		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			_ = tw.Close()
			_ = zw.Close()
			return PackResult{}, fmt.Errorf("cli: pack: tar header for %q: %w", rel, err)
		}
		// Use forward-slash paths regardless of OS.
		hdr.Name = filepath.ToSlash(rel)

		// Mask modes.
		switch {
		case fi.Mode().IsRegular():
			hdr.Mode = 0644
		case fi.IsDir():
			hdr.Mode = 0755
			// Ensure directory names end with / per tar convention.
			if !strings.HasSuffix(hdr.Name, "/") {
				hdr.Name += "/"
			}
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				slog.Warn("cli: pack: readlink failed, skipping symlink", "path", rel, "err", err)
				continue
			}
			hdr.Linkname = target
			hdr.Mode = 0777 // tar convention for symlinks (informational only)
		default:
			// Skip devices, pipes, sockets, etc.
			continue
		}

		if err := tw.WriteHeader(hdr); err != nil {
			_ = tw.Close()
			_ = zw.Close()
			return PackResult{}, fmt.Errorf("cli: pack: write header for %q: %w", rel, err)
		}

		if fi.Mode().IsRegular() {
			f, err := os.Open(abs) //nolint:gosec // path constructed from (rootPath, rel)
			if err != nil {
				_ = tw.Close()
				_ = zw.Close()
				return PackResult{}, fmt.Errorf("cli: pack: open %q: %w", rel, err)
			}
			_, copyErr := io.Copy(tw, f)
			_ = f.Close()
			if copyErr != nil {
				_ = tw.Close()
				_ = zw.Close()
				return PackResult{}, fmt.Errorf("cli: pack: copy %q: %w", rel, copyErr)
			}
		}

		// Check size cap after each entry; bytes may have flushed at any point.
		if opt.MaxBytes > 0 && sc.bytes > opt.MaxBytes {
			_ = tw.Close()
			_ = zw.Close()
			return PackResult{}, ErrTarballTooLarge
		}

		entryCount++
	}

	// Flush tar end-of-archive blocks.
	if err := tw.Close(); err != nil {
		_ = zw.Close()
		return PackResult{}, fmt.Errorf("cli: pack: close tar writer: %w", err)
	}
	// Flush zstd frame.
	if err := zw.Close(); err != nil {
		return PackResult{}, fmt.Errorf("cli: pack: close zstd writer: %w", err)
	}

	// Final size check — zstd.Close() flushes last bytes.
	if opt.MaxBytes > 0 && sc.bytes > opt.MaxBytes {
		return PackResult{}, ErrTarballTooLarge
	}

	return PackResult{
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:  sc.bytes,
		EntryCount: entryCount,
	}, nil
}

// --- file enumeration ---

// listFiles returns paths relative to rootPath for all files/dirs/symlinks that
// should be included in the archive.
func listFiles(rootPath string) ([]string, error) {
	gitDir := filepath.Join(rootPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return gitLsFiles(rootPath)
	}
	return walkWithIgnore(rootPath)
}

// gitLsFiles shells out to git to enumerate tracked and staged-but-untracked
// files, honouring .gitignore. Submodule recursion is omitted in v1.
func gitLsFiles(rootPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", rootPath,
		"ls-files",
		"--cached",
		"--others",
		"--exclude-standard",
	)
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	files := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			// Defensive: reject any entry that tries to escape the root.
			if strings.HasPrefix(l, "..") || filepath.IsAbs(l) {
				slog.Warn("cli: pack: git ls-files returned suspicious path, skipping", "path", l)
				continue
			}
			files = append(files, l)
		}
	}
	return files, nil
}

// walkWithIgnore enumerates rootPath with filepath.WalkDir, applying ignore
// patterns loaded from .dockerignore (if present) or the built-in fallback list.
func walkWithIgnore(rootPath string) ([]string, error) {
	ignores, err := loadDockerIgnore(rootPath)
	if err != nil {
		// Non-fatal: log a warning and proceed with the built-in list.
		slog.Warn("cli: pack: failed to load .dockerignore, using built-in ignore list", "err", err)
		ignores = builtinIgnores()
	}

	var files []string
	walkErr := filepath.WalkDir(rootPath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(rootPath, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // skip the root itself
		}
		if shouldIgnore(rel, d, ignores) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return files, nil
}

// hardcoded ignore list applied when no .dockerignore is present.
var defaultIgnores = []string{
	".git",
	"node_modules",
	"__pycache__",
	".venv",
	"target",
	"dist",
	"build",
	".DS_Store",
}

func builtinIgnores() []string {
	out := make([]string, len(defaultIgnores))
	copy(out, defaultIgnores)
	return out
}

// loadDockerIgnore reads rootPath/.dockerignore and returns the non-empty,
// non-comment lines as patterns. Returns (builtinIgnores(), nil) when the file
// does not exist.
func loadDockerIgnore(rootPath string) ([]string, error) {
	p := filepath.Join(rootPath, ".dockerignore")
	f, err := os.Open(p) //nolint:gosec // path is derived from rootPath
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return builtinIgnores(), nil
		}
		return builtinIgnores(), fmt.Errorf("open .dockerignore: %w", err)
	}
	defer func() { _ = f.Close() }()

	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := sc.Err(); err != nil {
		return builtinIgnores(), fmt.Errorf("read .dockerignore: %w", err)
	}
	return patterns, nil
}

// shouldIgnore returns true when rel matches one of the patterns.
// Matching is: exact equality OR the pattern is a prefix component of rel.
// No ** globs, no negation (v1 limitation — matches Docker's basic behaviour
// for the common case of "ignore a named directory").
func shouldIgnore(rel string, _ fs.DirEntry, patterns []string) bool {
	// Normalise to forward-slash for consistent matching across OSes.
	relFwd := filepath.ToSlash(rel)
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		patFwd := filepath.ToSlash(pat)
		if relFwd == patFwd {
			return true
		}
		// Prefix match: pattern "foo" should match "foo/bar.txt".
		if strings.HasPrefix(relFwd, patFwd+"/") {
			return true
		}
		// Simple glob: pattern "*.env" matches basename.
		base := filepath.Base(relFwd)
		if matched, _ := filepath.Match(patFwd, base); matched {
			return true
		}
	}
	return false
}

// --- helpers ---

// sizeCounterWriter counts bytes as they pass through.
type sizeCounterWriter struct {
	bytes int64
}

func (s *sizeCounterWriter) Write(p []byte) (int, error) {
	s.bytes += int64(len(p))
	return len(p), nil
}
