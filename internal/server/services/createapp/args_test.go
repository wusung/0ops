package createapp

import (
	"testing"
)

func TestAppCreateArgsValidate(t *testing.T) {
	tests := []struct {
		name    string
		args    *AppCreateArgs
		wantErr bool
	}{
		{
			name: "valid args",
			args: &AppCreateArgs{
				Slug:       "my-app",
				RepoURL:    "https://github.com/user/repo",
				Ref:        "main",
				Builder:    "dockerfile",
				DomainName: "example.com",
			},
			wantErr: false,
		},
		{
			name: "missing slug",
			args: &AppCreateArgs{
				RepoURL:    "https://github.com/user/repo",
				Ref:        "main",
				Builder:    "dockerfile",
				DomainName: "example.com",
			},
			wantErr: true,
		},
		{
			name: "invalid slug with uppercase",
			args: &AppCreateArgs{
				Slug:       "MyApp",
				RepoURL:    "https://github.com/user/repo",
				Ref:        "main",
				Builder:    "dockerfile",
				DomainName: "example.com",
			},
			wantErr: true,
		},
		{
			name: "invalid slug starting with dash",
			args: &AppCreateArgs{
				Slug:       "-app",
				RepoURL:    "https://github.com/user/repo",
				Ref:        "main",
				Builder:    "dockerfile",
				DomainName: "example.com",
			},
			wantErr: true,
		},
		{
			name: "missing repo_url",
			args: &AppCreateArgs{
				Slug:       "my-app",
				Ref:        "main",
				Builder:    "dockerfile",
				DomainName: "example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.args.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
