// Package githuboauth provides GitHub OAuth utilities.
package githuboauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	// ErrAuthorizationPending indicates the authorization is still pending.
	ErrAuthorizationPending = errors.New("authorization_pending")
	// ErrAccessDenied indicates access was denied.
	ErrAccessDenied = errors.New("access_denied")
	// ErrSlowDown indicates the client should slow down.
	ErrSlowDown = errors.New("slow_down")
	// ErrExpiredToken indicates the token has expired.
	ErrExpiredToken = errors.New("expired_token")
)

// DeviceAuthorization describes a GitHub Device Flow challenge.
type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresInSeconds        int
	IntervalSeconds         int
}

// AccessTokenResponse describes the device code exchange response.
type AccessTokenResponse struct {
	AccessToken string
	TokenType   string
	Scope       string
}

// UserProfile describes the GitHub user profile fetched after auth.
type UserProfile struct {
	Login string
	Name  string
	Email string
}

// Client talks to GitHub OAuth endpoints.
type Client struct {
	oauthBaseURL string
	apiBaseURL   string
	clientID     string
	clientSecret string
	httpClient   *http.Client
}

// NewClient constructs a GitHub OAuth client.
func NewClient(oauthBaseURL, apiBaseURL, clientID, clientSecret string, httpClient *http.Client) *Client {
	if strings.TrimSpace(oauthBaseURL) == "" {
		oauthBaseURL = "https://github.com"
	}
	if strings.TrimSpace(apiBaseURL) == "" {
		apiBaseURL = "https://api.github.com"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		oauthBaseURL: strings.TrimRight(oauthBaseURL, "/"),
		apiBaseURL:   strings.TrimRight(apiBaseURL, "/"),
		clientID:     strings.TrimSpace(clientID),
		clientSecret: strings.TrimSpace(clientSecret),
		httpClient:   httpClient,
	}
}

// NewClientFromEnv loads the client configuration from environment variables.
func NewClientFromEnv(httpClient *http.Client) (*Client, error) {
	clientID := strings.TrimSpace(os.Getenv("GITHUB_OAUTH_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"))
	if clientID == "" {
		return nil, fmt.Errorf("GITHUB_OAUTH_CLIENT_ID is required")
	}
	if clientSecret == "" {
		return nil, fmt.Errorf("GITHUB_OAUTH_CLIENT_SECRET is required")
	}
	return NewClient(
		os.Getenv("GITHUB_OAUTH_BASE_URL"),
		os.Getenv("GITHUB_API_BASE_URL"),
		clientID,
		clientSecret,
		httpClient,
	), nil
}

// StartDeviceAuthorization begins the GitHub device flow.
func (c *Client) StartDeviceAuthorization(ctx context.Context) (DeviceAuthorization, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("scope", "user:email")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthBaseURL+"/login/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuthorization{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "0ops-server")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return DeviceAuthorization{}, c.decodeOAuthError(res.Body, res.StatusCode)
	}

	var payload struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return DeviceAuthorization{}, err
	}

	return DeviceAuthorization{
		DeviceCode:              payload.DeviceCode,
		UserCode:                payload.UserCode,
		VerificationURI:         payload.VerificationURI,
		VerificationURIComplete: payload.VerificationURIComplete,
		ExpiresInSeconds:        payload.ExpiresIn,
		IntervalSeconds:         payload.Interval,
	}, nil
}

// ExchangeDeviceCode swaps a device code for an access token.
func (c *Client) ExchangeDeviceCode(ctx context.Context, deviceCode string) (AccessTokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthBaseURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return AccessTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "0ops-server")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return AccessTokenResponse{}, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return AccessTokenResponse{}, c.decodeOAuthError(res.Body, res.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
		ErrorURI    string `json:"error_uri"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return AccessTokenResponse{}, err
	}
	switch payload.Error {
	case "":
		return AccessTokenResponse{
			AccessToken: payload.AccessToken,
			TokenType:   payload.TokenType,
			Scope:       payload.Scope,
		}, nil
	case "authorization_pending":
		return AccessTokenResponse{}, ErrAuthorizationPending
	case "access_denied":
		return AccessTokenResponse{}, ErrAccessDenied
	case "slow_down":
		return AccessTokenResponse{}, ErrSlowDown
	case "expired_token":
		return AccessTokenResponse{}, ErrExpiredToken
	default:
		return AccessTokenResponse{}, fmt.Errorf("github oauth error: %s", payload.Error)
	}
}

// FetchUser loads the GitHub user profile for the issued access token.
func (c *Client) FetchUser(ctx context.Context, accessToken string) (UserProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/user", nil)
	if err != nil {
		return UserProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "0ops-server")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return UserProfile{}, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return UserProfile{}, c.decodeOAuthError(res.Body, res.StatusCode)
	}

	var payload struct {
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return UserProfile{}, err
	}
	return UserProfile{Login: payload.Login, Name: payload.Name, Email: payload.Email}, nil
}

func (c *Client) decodeOAuthError(body io.Reader, statusCode int) error {
	var payload struct {
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
		ErrorURI  string `json:"error_uri"`
		Message   string `json:"message"`
	}
	data, _ := io.ReadAll(body)
	if len(bytes.TrimSpace(data)) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	if payload.Error != "" {
		switch payload.Error {
		case "authorization_pending":
			return ErrAuthorizationPending
		case "access_denied":
			return ErrAccessDenied
		case "slow_down":
			return ErrSlowDown
		case "expired_token":
			return ErrExpiredToken
		default:
			if payload.ErrorDesc != "" {
				return fmt.Errorf("github oauth %s: %s", payload.Error, payload.ErrorDesc)
			}
			return fmt.Errorf("github oauth %s", payload.Error)
		}
	}
	if payload.Message != "" {
		return fmt.Errorf("github api %d: %s", statusCode, payload.Message)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		return fmt.Errorf("github oauth %d: %s", statusCode, string(bytes.TrimSpace(data)))
	}
	return fmt.Errorf("github oauth request failed: status %d", statusCode)
}
