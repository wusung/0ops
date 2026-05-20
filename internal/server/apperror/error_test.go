package apperror

import "testing"

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
