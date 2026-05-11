package authconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File is the shared auth.json document.
type File struct {
	Version int     `json:"version"`
	Tokens  []Token `json:"tokens"`
}

// Token is a single host entry in auth.json.
type Token struct {
	Host            string    `json:"host"`
	GitHubLogin     string    `json:"github_login,omitempty"`
	DefaultTeamSlug string    `json:"default_team_slug,omitempty"`
	BearerToken     string    `json:"bearer_token,omitempty"`
	IssuedAt        time.Time `json:"issued_at,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
}

// Load reads the shared auth.json file.
func Load() (File, error) {
	path, err := Path()
	if err != nil {
		return File{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}

	var out File
	if err := json.Unmarshal(data, &out); err != nil {
		return File{}, err
	}
	return out, nil
}

// Save writes the shared auth.json file.
func Save(file File) error {
	if file.Version == 0 {
		file.Version = 1
	}

	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Path returns the canonical auth.json path.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "0ops", "auth.json"), nil
}

// First returns the first token entry.
func (f File) First() (Token, bool) {
	if len(f.Tokens) == 0 {
		return Token{}, false
	}
	return f.Tokens[0], true
}

// TokenForHost returns the token for a given host.
func (f File) TokenForHost(host string) (Token, bool) {
	host = normalizeHost(host)
	if host == "" {
		return f.First()
	}
	for _, token := range f.Tokens {
		if normalizeHost(token.Host) == host {
			return token, true
		}
	}
	return Token{}, false
}

// DefaultTeamForHost returns the default team slug for a host.
func (f File) DefaultTeamForHost(host string) (string, bool) {
	token, ok := f.TokenForHost(host)
	if !ok || strings.TrimSpace(token.DefaultTeamSlug) == "" {
		return "", false
	}
	return token.DefaultTeamSlug, true
}

// BearerForHost returns the bearer token for a host.
func (f File) BearerForHost(host string) (string, bool) {
	token, ok := f.TokenForHost(host)
	if !ok || strings.TrimSpace(token.BearerToken) == "" {
		return "", false
	}
	return token.BearerToken, true
}

// SetDefaultTeamForHost updates the default team slug for the matching host entry.
func (f *File) SetDefaultTeamForHost(host, teamSlug string) bool {
	host = normalizeHost(host)
	if host == "" {
		if len(f.Tokens) == 0 {
			return false
		}
		f.Tokens[0].DefaultTeamSlug = teamSlug
		return true
	}

	for i := range f.Tokens {
		if normalizeHost(f.Tokens[i].Host) == host {
			f.Tokens[i].DefaultTeamSlug = teamSlug
			return true
		}
	}
	return false
}

// UpsertTokenForHost inserts or replaces a host auth entry.
func (f *File) UpsertTokenForHost(token Token) {
	token.Host = normalizeHost(token.Host)
	if token.Host == "" {
		return
	}
	for i := range f.Tokens {
		if normalizeHost(f.Tokens[i].Host) == token.Host {
			f.Tokens[i] = token
			return
		}
	}
	f.Tokens = append(f.Tokens, token)
}

// RemoveHost removes a host auth entry.
func (f *File) RemoveHost(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	for i := range f.Tokens {
		if normalizeHost(f.Tokens[i].Host) == host {
			f.Tokens = append(f.Tokens[:i], f.Tokens[i+1:]...)
			return true
		}
	}
	return false
}

func normalizeHost(v string) string {
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

var ErrNotFound = errors.New("auth config not found")
