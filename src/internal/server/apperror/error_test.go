package apperror

import "testing"

// TestSourceCodesStable is intentionally redundant: each map entry repeats the
// constant value on both sides. The test fails if any Code* constant value is
// changed (e.g., a refactor renaming "source_required" to "source-required"
// would break this test before it breaks any deployed client). This is the
// wire-contract lock for the M6 error codes.
func TestSourceCodesStable(t *testing.T) {
	tests := map[string]string{
		"source_required":            CodeSourceRequired,
		"source_conflict":            CodeSourceConflict,
		"source_invalid":             CodeSourceInvalid,
		"source_kind_unsupported":    CodeSourceKindUnsupported,
		"unsupported_source":         CodeUnsupportedSource,
		"payload_too_large":          CodePayloadTooLarge,
		"unsupported_archive_format": CodeUnsupportedArchive,
		"archive_corrupt":            CodeArchiveCorrupt,
		"sha256_mismatch":            CodeSHA256Mismatch,
		"upload_rate_limited":        CodeUploadRateLimited,
		"team_quota_exceeded":        CodeTeamQuotaExceeded,
		"source_not_found":           CodeSourceNotFound,
		"source_expired":             CodeSourceExpired,
		"upload_cross_team":          CodeUploadCrossTeam,
	}
	for want, got := range tests {
		if got != want {
			t.Errorf("code mismatch: got %q, want %q", got, want)
		}
	}
}
