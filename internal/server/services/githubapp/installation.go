package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AccessTokenResponse describes GitHub's installation access token response.
type AccessTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Scope     string    `json:"permissions,omitempty"`
}

// InstallationTokenClient exchanges App JWT for installation access tokens.
type InstallationTokenClient struct {
	signer     *JWTSigner
	apiBaseURL string
	httpClient *http.Client
}

// NewInstallationTokenClient constructs a client to fetch installation tokens.
func NewInstallationTokenClient(signer *JWTSigner, apiBaseURL string, httpClient *http.Client) *InstallationTokenClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &InstallationTokenClient{
		signer:     signer,
		apiBaseURL: apiBaseURL,
		httpClient: httpClient,
	}
}

// GetAccessToken exchanges App JWT for an installation access token.
// GitHub returns a token valid for 1 hour; this should be cached.
func (c *InstallationTokenClient) GetAccessToken(ctx context.Context, installID int64) (AccessTokenResponse, error) {
	if c.signer == nil {
		return AccessTokenResponse{}, ErrMissingPrivateKey
	}

	appJWT, err := c.signer.Sign(10 * time.Minute)
	if err != nil {
		return AccessTokenResponse{}, fmt.Errorf("sign app jwt: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBaseURL, installID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return AccessTokenResponse{}, err
	}

	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "0ops-server")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AccessTokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return AccessTokenResponse{}, fmt.Errorf("github api %d: %s", resp.StatusCode, string(body))
	}

	var result AccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return AccessTokenResponse{}, err
	}

	return result, nil
}

// DeleteInstallation removes the installation server-side via the GitHub API
// (uninstall-flow spec § 5.1). HTTP 204 / 404 are treated as success because
// the user-controlled installation may already be gone.
func (c *InstallationTokenClient) DeleteInstallation(ctx context.Context, installID int64) error {
	if c.signer == nil {
		return ErrMissingPrivateKey
	}

	appJWT, err := c.signer.Sign(10 * time.Minute)
	if err != nil {
		return fmt.Errorf("sign app jwt: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d", c.apiBaseURL, installID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "0ops-server")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("github api %d: %s", resp.StatusCode, string(body))
}
