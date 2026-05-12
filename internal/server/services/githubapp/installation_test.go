package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInstallationTokenClientGetAccessToken(t *testing.T) {
	t.Parallel()

	key := mustRSAPrivateKey(t)
	signer := NewJWTSigner(98765, key)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/123/access_tokens" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Fatalf("missing Authorization header")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghu_installation_token",
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	t.Cleanup(srv.Close)

	client := NewInstallationTokenClient(signer, srv.URL, srv.Client())

	resp, err := client.GetAccessToken(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetAccessToken() error = %v", err)
	}
	if resp.Token != "ghu_installation_token" {
		t.Fatalf("token = %q", resp.Token)
	}
	if resp.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt is zero")
	}
}

func TestInstallationTokenClientMissingJWTSigner(t *testing.T) {
	t.Parallel()

	client := NewInstallationTokenClient(nil, "https://api.github.com", nil)

	_, err := client.GetAccessToken(context.Background(), 123)
	if err != ErrMissingPrivateKey {
		t.Fatalf("GetAccessToken() error = %v, want ErrMissingPrivateKey", err)
	}
}
