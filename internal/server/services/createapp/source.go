package createapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// ErrSourceUnsupported is returned when SourceToInspectURL receives a Source
// whose Type is not recognised. This is distinct from validation-time errors
// (which T11 will surface as 422 source_kind_unsupported); reaching this
// indicates a programming error (validator should have rejected upstream).
var ErrSourceUnsupported = errors.New("createapp: source kind unsupported")

// SourceToInspectURL converts a Source DTO into the URL string consumed by
// the existing Inspector interface. The mapping is:
//
//	github → src.GitHub.URL (as-is; e.g. "https://github.com/foo/bar")
//	upload → "upload://<upload_id>"
//
// The function is the bridge between the new sum-type API (T2) and the
// legacy URL-scheme Inspector dispatch. It is intentionally minimal — the
// heavy lifting is in the inspector implementations.
func SourceToInspectURL(src dto.Source) (string, error) {
	switch src.Type {
	case dto.SourceKindGitHub:
		if src.GitHub == nil {
			return "", fmt.Errorf("github source missing payload: %w", ErrSourceUnsupported)
		}
		return src.GitHub.URL, nil
	case dto.SourceKindUpload:
		if src.Upload == nil {
			return "", fmt.Errorf("upload source missing payload: %w", ErrSourceUnsupported)
		}
		return "upload://" + src.Upload.UploadID, nil
	default:
		return "", fmt.Errorf("source kind %q: %w", src.Type, ErrSourceUnsupported)
	}
}

// SourceRef extracts the ref field from a Source. github source carries Ref
// in SourceGitHub.Ref; upload source carries an optional audit ref in
// SourceUpload.Ref (not used for build dispatch but useful for logging).
func SourceRef(src dto.Source) string {
	switch src.Type {
	case dto.SourceKindGitHub:
		if src.GitHub != nil {
			return src.GitHub.Ref
		}
	case dto.SourceKindUpload:
		if src.Upload != nil {
			return src.Upload.Ref
		}
	}
	return ""
}

// SourceFactory wraps a URL-dispatch Inspector and exposes a Source-typed
// API for callers that already work in terms of dto.Source. Future T11/T12
// will use this in createapp.Service. Existing call sites that already pass
// repoURL strings continue to use the Inspector directly.
type SourceFactory struct {
	Inspector Inspector
}

// Inspect normalises the Source to a URL and ref, then delegates to the
// underlying Inspector.
func (f SourceFactory) Inspect(ctx context.Context, src dto.Source) (RepoMetadata, error) {
	url, err := SourceToInspectURL(src)
	if err != nil {
		return RepoMetadata{}, err
	}
	if f.Inspector == nil {
		return RepoMetadata{}, errors.New("createapp: SourceFactory has nil Inspector")
	}
	return f.Inspector.Inspect(ctx, url, SourceRef(src))
}
