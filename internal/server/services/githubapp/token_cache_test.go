package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenCacheGetAndSet(t *testing.T) {
	t.Parallel()

	cache := NewTokenCache()

	// Initially empty
	if cached := cache.Get(123); cached != nil {
		t.Fatalf("Get() should return nil for empty cache")
	}

	// Set and retrieve
	token := &CachedToken{
		Token:     "ghu_test_token",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	cache.Set(123, token)

	if cached := cache.Get(123); cached == nil || cached.Token != "ghu_test_token" {
		t.Fatalf("Get() after Set() failed")
	}
}

func TestTokenCacheExpiry(t *testing.T) {
	t.Parallel()

	cache := NewTokenCache()

	// Set expired token
	token := &CachedToken{
		Token:     "ghu_expired",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // Already expired
	}
	cache.Set(123, token)

	// Should return nil (expired)
	if cached := cache.Get(123); cached != nil {
		t.Fatalf("Get() should return nil for expired token")
	}
}

func TestTokenProviderCacheHit(t *testing.T) {
	t.Parallel()

	key := mustRSAPrivateKey(t)
	signer := NewJWTSigner(98765, key)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghu_token_" + string(rune(callCount+48)), //nolint:gosec // callCount fits in rune
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	t.Cleanup(srv.Close)

	client := NewInstallationTokenClient(signer, srv.URL, srv.Client())
	cache := NewTokenCache()
	provider := NewTokenProvider(client, cache)

	// First call fetches from GitHub
	token1, err := provider.GetToken(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetToken() error = %v", err)
	}
	if token1 != "ghu_token_1" {
		t.Fatalf("token1 = %q", token1)
	}

	// Second call uses cache
	token2, err := provider.GetToken(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetToken() error = %v", err)
	}
	if token2 != "ghu_token_1" {
		t.Fatalf("token2 = %q, should be from cache", token2)
	}

	// Should have called GitHub only once
	if callCount != 1 {
		t.Fatalf("callCount = %d, want 1", callCount)
	}
}

func TestTokenProviderRefreshStaleTokens(t *testing.T) {
	t.Parallel()

	key := mustRSAPrivateKey(t)
	signer := NewJWTSigner(98765, key)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghu_token",
			"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339),
		})
	}))
	t.Cleanup(srv.Close)

	client := NewInstallationTokenClient(signer, srv.URL, srv.Client())
	cache := NewTokenCache()
	provider := NewTokenProvider(client, cache)

	// Set a stale token (expiring in 5 minutes)
	cache.Set(123, &CachedToken{
		Token:     "old_token",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})

	// Refresh should fetch new token
	err := provider.RefreshStaleTokens(context.Background())
	if err != nil {
		t.Fatalf("RefreshStaleTokens() error = %v", err)
	}

	// New token should be in cache
	cached := cache.Get(123)
	if cached == nil || cached.Token != "ghu_token" {
		t.Fatalf("RefreshStaleTokens() did not update cache")
	}
}

func TestCachedTokenIsExpired(t *testing.T) {
	t.Parallel()

	t.Run("valid_token", func(t *testing.T) {
		token := &CachedToken{
			Token:     "ghu_token",
			ExpiresAt: time.Now().Add(2 * time.Hour),
		}
		if token.IsExpired(0) {
			t.Fatalf("IsExpired(0) should be false for future token")
		}
		if token.IsExpired(1 * time.Hour) {
			t.Fatalf("IsExpired(1h) should be false for 2h future token with 1h buffer")
		}
	})

	t.Run("expired_token", func(t *testing.T) {
		token := &CachedToken{
			Token:     "ghu_token",
			ExpiresAt: time.Now().Add(-1 * time.Hour),
		}
		if !token.IsExpired(0) {
			t.Fatalf("IsExpired(0) should be true for past token")
		}
	})

	t.Run("nil_token", func(t *testing.T) {
		var token *CachedToken
		if !token.IsExpired(0) {
			t.Fatalf("IsExpired(0) on nil should be true")
		}
	})
}
