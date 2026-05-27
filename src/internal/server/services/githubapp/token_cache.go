package githubapp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CachedToken represents a cached installation token with expiry.
type CachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// IsExpired reports whether the token is expired (or close to expiring).
func (ct *CachedToken) IsExpired(buffer time.Duration) bool {
	return ct == nil || time.Now().Add(buffer).After(ct.ExpiresAt)
}

// TokenCache caches installation tokens in memory.
type TokenCache struct {
	mu    sync.RWMutex
	cache map[int64]*CachedToken
}

// NewTokenCache constructs an empty token cache.
func NewTokenCache() *TokenCache {
	return &TokenCache{
		cache: make(map[int64]*CachedToken),
	}
}

// Get retrieves a cached token if valid (with 10min buffer).
func (tc *TokenCache) Get(installID int64) *CachedToken {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	cached := tc.cache[installID]
	if cached.IsExpired(10 * time.Minute) {
		return nil
	}
	return cached
}

// Set stores a token in the cache.
func (tc *TokenCache) Set(installID int64, token *CachedToken) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.cache[installID] = token
}

// Invalidate removes a cached token.
func (tc *TokenCache) Invalidate(installID int64) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	delete(tc.cache, installID)
}

// TokenProvider is the public interface for fetching installation tokens with auto-cache.
type TokenProvider struct {
	client *InstallationTokenClient
	cache  *TokenCache
	mu     sync.Mutex
}

// NewTokenProvider constructs a token provider with built-in caching.
func NewTokenProvider(client *InstallationTokenClient, cache *TokenCache) *TokenProvider {
	return &TokenProvider{
		client: client,
		cache:  cache,
	}
}

// GetToken returns a cached token if valid, otherwise fetches and caches a new one.
func (tp *TokenProvider) GetToken(ctx context.Context, installID int64) (string, error) {
	// Fast path: check cache with read lock
	if cached := tp.cache.Get(installID); cached != nil {
		return cached.Token, nil
	}

	// Slow path: fetch from GitHub (serialize concurrent requests)
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Double-check after acquiring write lock
	if cached := tp.cache.Get(installID); cached != nil {
		return cached.Token, nil
	}

	resp, err := tp.client.GetAccessToken(ctx, installID)
	if err != nil {
		return "", fmt.Errorf("get access token: %w", err)
	}

	tp.cache.Set(installID, &CachedToken{
		Token:     resp.Token,
		ExpiresAt: resp.ExpiresAt,
	})

	return resp.Token, nil
}

// RefreshStaleTokens refreshes tokens that expire within 10 minutes.
// This should be called periodically (e.g., every 1 minute) by a background goroutine.
func (tp *TokenProvider) RefreshStaleTokens(ctx context.Context) error {
	tp.cache.mu.RLock()
	installIDs := make([]int64, 0, len(tp.cache.cache))
	for id, cached := range tp.cache.cache {
		if cached.IsExpired(10 * time.Minute) {
			installIDs = append(installIDs, id)
		}
	}
	tp.cache.mu.RUnlock()

	// Refresh each stale token
	for _, id := range installIDs {
		_, _ = tp.GetToken(ctx, id)
	}

	return nil
}
