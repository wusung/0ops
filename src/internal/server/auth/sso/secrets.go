package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// parseTokenResponse extracts the id_token and derives an absolute session
// expiry from the token endpoint response (expires_in seconds, if present).
func parseTokenResponse(body []byte) (string, time.Time, error) {
	var tr struct {
		IDToken   string `json:"id_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, fmt.Errorf("token response decode: %w", err)
	}
	if tr.IDToken == "" {
		return "", time.Time{}, errors.New("token response: missing id_token")
	}
	var expiry time.Time
	if tr.ExpiresIn > 0 {
		expiry = time.Now().UTC().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return tr.IDToken, expiry, nil
}

// ErrSecretNotFound is returned when a client_secret_ref has no stored secret.
var ErrSecretNotFound = errors.New("sso: secret not found for ref")

// MemorySecretStore is a process-local SecretStore. It keeps the IdP client
// secret out of the DB (hard rule #7 — DB only holds the ref). Durable,
// encrypted at-rest storage is deferred to the secrets-management feature; this
// default is sufficient for a single backend process and for tests.
type MemorySecretStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewMemorySecretStore builds an empty in-memory secret store.
func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{secrets: make(map[string]string)}
}

// Put stores a secret under ref.
func (m *MemorySecretStore) Put(ref, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[ref] = secret
	return nil
}

// Get resolves a secret by ref, or ErrSecretNotFound.
func (m *MemorySecretStore) Get(ref string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.secrets[ref]
	if !ok {
		return "", ErrSecretNotFound
	}
	return s, nil
}

// MemoryStateStore is a process-local one-time StateStore for OIDC state/PKCE
// correlation. Entries are consumed on first read.
type MemoryStateStore struct {
	mu    sync.Mutex
	items map[string]StateData
}

// NewMemoryStateStore builds an empty in-memory state store.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{items: make(map[string]StateData)}
}

// Save records the state correlation payload.
func (m *MemoryStateStore) Save(state string, data StateData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[state] = data
}

// Consume returns and deletes the payload for state (one-time use).
func (m *MemoryStateStore) Consume(state string) (StateData, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.items[state]
	if ok {
		delete(m.items, state)
	}
	return d, ok
}

// HTTPCodeExchanger performs the real OIDC authorization-code exchange at the
// IdP token endpoint. It is deferred-validation: a live IdP + valid client
// secret are required, so `manage.sh test` exercises the callback via an
// injected stub instead (spec § 8.1).
type HTTPCodeExchanger struct {
	Client *http.Client
}

// NewHTTPCodeExchanger builds an exchanger; nil client falls back to a default.
func NewHTTPCodeExchanger(client *http.Client) *HTTPCodeExchanger {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPCodeExchanger{Client: client}
}

// Exchange swaps an authorization code for the raw id_token.
func (e *HTTPCodeExchanger) Exchange(ctx context.Context, disc Discovery, clientID, clientSecret, code, redirectURI, codeVerifier string) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, disc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := e.Client.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("token exchange: status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, err
	}
	idToken, expiry, err := parseTokenResponse(body)
	if err != nil {
		return "", time.Time{}, err
	}
	return idToken, expiry, nil
}
