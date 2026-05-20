package cli

import "testing"

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
