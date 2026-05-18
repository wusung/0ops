package createapp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRepoURL_GitHubScheme(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"", true},
		{"https://github.com/owner/repo", false},
		{"git@github.com:owner/repo.git", false},
		{"https://gitlab.com/owner/repo", true},
		{"https://github.com/owner", true},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
			err := validateRepoURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateRepoURL(%q) err=%v wantErr=%v", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestValidateRepoURL_FileScheme(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCAL_FILE_REPO_ROOT", root)

	repoDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		gateOn    bool
		url       string
		wantErr   error
	}{
		{"gate off rejects file://", false, "file://" + repoDir, ErrLocalFileRepoDisabled},
		{"gate on accepts in-root", true, "file://" + repoDir, nil},
		{"gate on rejects escape", true, "file:///etc/passwd", ErrRepoPathInvalid},
		{"gate on rejects non-absolute", true, "file://relative/path", ErrRepoPathInvalid},
		{"gate on rejects missing path", true, "file:///tmp/does-not-exist-xyz-m56", ErrRepoPathNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gateOn {
				t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
			} else {
				t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
			}
			err := validateRepoURL(tc.url)
			switch {
			case tc.wantErr == nil:
				if err != nil {
					t.Fatalf("got err=%v want nil", err)
				}
			default:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got err=%v want errors.Is=%v", err, tc.wantErr)
				}
			}
		})
	}
}
