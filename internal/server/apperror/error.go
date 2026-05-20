// Package apperror provides error handling utilities.
package apperror

import (
	"encoding/json"
	"net/http"
)

// Class groups errors into HTTP classes.
type Class string

const (
	// ClassBadRequest indicates a bad request error.
	ClassBadRequest Class = "bad_request"
	// ClassUnauthorized indicates an unauthorized error.
	ClassUnauthorized Class = "unauthorized"
	// ClassForbidden indicates a forbidden error.
	ClassForbidden Class = "forbidden"
	// ClassNotFound indicates a not found error.
	ClassNotFound Class = "not_found"
	// ClassConflict indicates a conflict error.
	ClassConflict Class = "conflict"
	// ClassUnprocessable indicates a semantically invalid request (HTTP 422).
	ClassUnprocessable Class = "unprocessable"
	// ClassTooManyRequests indicates a rate-limited error.
	ClassTooManyRequests Class = "too_many_requests"
	// ClassInternal indicates an internal error.
	ClassInternal Class = "internal"
	// ClassUnavailable indicates an upstream/dependency outage.
	ClassUnavailable Class = "unavailable"
)

// Source / upload validation codes used by the app-source-ingestion
// feature (ADR-0013). String values are part of the stable API contract;
// tests in error_test.go assert each constant matches its string key.
const (
	// CodeSourceRequired: no source provided (neither Source nor RepoURL).
	CodeSourceRequired = "source_required"
	// CodeSourceConflict: caller provided both Source and RepoURL; pick one.
	CodeSourceConflict = "source_conflict"
	// CodeSourceInvalid: Source.Type set but its kind-specific payload is missing or malformed.
	CodeSourceInvalid = "source_invalid"
	// CodeSourceKindUnsupported: Source.Type carries an enum value the server doesn't recognise.
	CodeSourceKindUnsupported = "source_kind_unsupported"
	// CodeUnsupportedSource: RepoURL scheme is not supported (e.g., file:// in production, or an unknown scheme).
	CodeUnsupportedSource = "unsupported_source"
	// CodePayloadTooLarge: upload archive exceeds APP_SOURCE_UPLOAD_MAX_BYTES.
	CodePayloadTooLarge = "payload_too_large"
	// CodeUnsupportedArchive: upload Content-Type / magic byte not one of tar.zst / tar.gz.
	CodeUnsupportedArchive = "unsupported_archive_format"
	// CodeArchiveCorrupt: archive failed to decompress / detar (path traversal, bad zstd, etc).
	CodeArchiveCorrupt = "archive_corrupt"
	// CodeSHA256Mismatch: server-computed sha256 differs from the client-supplied form field.
	CodeSHA256Mismatch = "sha256_mismatch"
	// CodeUploadRateLimited: caller exceeded per-team upload requests-per-window.
	CodeUploadRateLimited = "upload_rate_limited"
	// CodeTeamQuotaExceeded: per-team aggregate (inert bytes, pinned count, daily uploads) exceeded plan tier.
	CodeTeamQuotaExceeded = "team_quota_exceeded"
	// CodeSourceNotFound: upload_id does not exist (or is not visible to caller's team).
	CodeSourceNotFound = "source_not_found"
	// CodeSourceExpired: upload existed but its TTL (received_at + 24h, or pinned_at + 7d) has elapsed.
	CodeSourceExpired = "source_expired"
	// CodeUploadCrossTeam: caller is authenticated but the upload belongs to a different team.
	CodeUploadCrossTeam = "upload_cross_team"
)

// Error is the server error envelope source.
type Error struct {
	Class   Class
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// New creates a typed application error.
func New(class Class, code, message string, details map[string]any) *Error {
	return &Error{Class: class, Code: code, Message: message, Details: details}
}

// Status maps a class to HTTP status.
func Status(class Class) int {
	switch class {
	case ClassBadRequest:
		return http.StatusBadRequest
	case ClassUnauthorized:
		return http.StatusUnauthorized
	case ClassForbidden:
		return http.StatusForbidden
	case ClassNotFound:
		return http.StatusNotFound
	case ClassConflict:
		return http.StatusConflict
	case ClassUnprocessable:
		return http.StatusUnprocessableEntity
	case ClassTooManyRequests:
		return http.StatusTooManyRequests
	case ClassInternal:
		return http.StatusInternalServerError
	case ClassUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Write writes a JSON error response.
func Write(w http.ResponseWriter, code string, class Class, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(Status(class))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}
