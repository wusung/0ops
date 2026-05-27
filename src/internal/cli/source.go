package cli

import (
	"errors"
	"fmt"
	"strings"
)

// sourceKind is the discriminated enum for --source flag values.
type sourceKind int

const (
	sourceKindUnset      sourceKind = iota
	sourceKindLocalPath             // bare path: ./mydir, /abs/path, mydir
	sourceKindUploadID              // upload://upl_xxx
	sourceKindGitHubURL             // https://github.com/... or git@github.com:...
	sourceKindFileURL               // file:// — dev legacy (ADR-0012)
)

// classifySource inspects s and returns the sourceKind.
// The empty string returns sourceKindUnset; unknown URL schemes return sourceKindUnset.
func classifySource(s string) sourceKind {
	s = strings.TrimSpace(s)
	if s == "" {
		return sourceKindUnset
	}
	switch {
	case strings.HasPrefix(s, "upload://"):
		return sourceKindUploadID
	case strings.HasPrefix(s, "https://github.com/"), strings.HasPrefix(s, "git@github.com:"):
		return sourceKindGitHubURL
	case strings.HasPrefix(s, "file://"):
		return sourceKindFileURL
	case strings.HasPrefix(s, "./"), strings.HasPrefix(s, "../"), strings.HasPrefix(s, "/"),
		s == ".", s == "..":
		return sourceKindLocalPath
	case !strings.Contains(s, "://") && !strings.Contains(s, "@"):
		// Bare name with no scheme or SSH syntax — treat as relative path.
		return sourceKindLocalPath
	}
	return sourceKindUnset
}

// wrapUploadError converts *UploadError, ErrTarballTooLarge, and
// ErrTooManyEntries into user-facing messages with actionable hints.
func wrapUploadError(err error) error {
	if err == nil {
		return nil
	}
	var ue *UploadError
	if errors.As(err, &ue) {
		switch ue.Code {
		case "payload_too_large":
			return fmt.Errorf("upload too large; add a .dockerignore to exclude vendor directories or raise --upload-max-bytes: %w", ue)
		case "team_quota_exceeded":
			return fmt.Errorf("team upload quota exceeded; wait for previous uploads to expire or upgrade your plan: %w", ue)
		case "unauthorized":
			return fmt.Errorf("unauthorized; run `0ops auth login` to refresh your token: %w", ue)
		case "unsupported_archive_format":
			return fmt.Errorf("server rejected archive format; this is a CLI bug, please report: %w", ue)
		default:
			return fmt.Errorf("upload failed: %w", ue)
		}
	}
	if errors.Is(err, ErrTarballTooLarge) {
		return fmt.Errorf("source directory too large to pack: %w. "+
			"Use .dockerignore to exclude generated files.", err)
	}
	if errors.Is(err, ErrTooManyEntries) {
		return fmt.Errorf("source directory has too many files to pack: %w. "+
			"Use .dockerignore to exclude generated directories.", err)
	}
	return fmt.Errorf("upload: %w", err)
}
