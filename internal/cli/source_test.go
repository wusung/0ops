package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapUploadError_PreservesUploadErrorChain(t *testing.T) {
	in := &UploadError{HTTPStatus: 413, Code: "payload_too_large", Message: "too big"}
	out := wrapUploadError(in)
	var ue *UploadError
	if !errors.As(out, &ue) {
		t.Fatalf("expected errors.As to find *UploadError, got %T", out)
	}
	if ue.Code != "payload_too_large" {
		t.Errorf("Code: got %q want %q", ue.Code, "payload_too_large")
	}
	// Friendly hint should be in the message.
	if !strings.Contains(out.Error(), ".dockerignore") {
		t.Errorf("expected friendly hint in error message, got %q", out.Error())
	}
}

func TestClassifySource(t *testing.T) {
	tests := []struct {
		input string
		want  sourceKind
	}{
		{"", sourceKindUnset},
		{"./mydir", sourceKindLocalPath},
		{"../parent", sourceKindLocalPath},
		{"/abs/path", sourceKindLocalPath},
		{".", sourceKindLocalPath},
		{"..", sourceKindLocalPath},
		{"mydir", sourceKindLocalPath},
		{"upload://upl_xxx", sourceKindUploadID},
		{"upload://", sourceKindUploadID}, // validation handled downstream
		{"https://github.com/foo/bar", sourceKindGitHubURL},
		{"git@github.com:foo/bar.git", sourceKindGitHubURL},
		{"file:///workspace/app", sourceKindFileURL},
		{"https://gitlab.com/foo/bar", sourceKindUnset},
		{"http://example.com", sourceKindUnset},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			if got := classifySource(tc.input); got != tc.want {
				t.Errorf("classifySource(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}
