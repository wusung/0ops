package backendclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// Client calls the 0ops backend.
type Client struct {
	BaseURL     string
	BearerToken string
	HTTP        *http.Client
}

// New returns a client with sensible defaults.
func New(baseURL, bearerToken string) *Client {
	return &Client{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		BearerToken: bearerToken,
		HTTP:        &http.Client{Timeout: 15 * time.Second},
	}
}

// ListApps fetches a team-scoped apps page.
func (c *Client) ListApps(ctx context.Context, teamSlug string, pageSize int, cursor string) (dto.ListAppsResponse, error) {
	if pageSize <= 0 {
		pageSize = 50
	}

	endpoint, err := url.Parse(c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/apps")
	if err != nil {
		return dto.ListAppsResponse{}, err
	}
	q := endpoint.Query()
	q.Set("page_size", fmt.Sprintf("%d", pageSize))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return dto.ListAppsResponse{}, err
	}
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}

	res, err := c.httpClient().Do(req)
	if err != nil {
		return dto.ListAppsResponse{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return dto.ListAppsResponse{}, decodeError(res)
	}

	var out dto.ListAppsResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return dto.ListAppsResponse{}, err
	}
	return out, nil
}

// GetApp fetches a team-scoped app by slug.
func (c *Client) GetApp(ctx context.Context, teamSlug, appSlug string) (dto.AppRef, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/apps/" + url.PathEscape(appSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return dto.AppRef{}, err
	}
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}

	res, err := c.httpClient().Do(req)
	if err != nil {
		return dto.AppRef{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return dto.AppRef{}, decodeError(res)
	}

	var out dto.AppRef
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return dto.AppRef{}, err
	}
	return out, nil
}

// ListTeams fetches the current actor's teams.
func (c *Client) ListTeams(ctx context.Context) (dto.ListTeamsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/me/teams", nil)
	if err != nil {
		return dto.ListTeamsResponse{}, err
	}
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}

	res, err := c.httpClient().Do(req)
	if err != nil {
		return dto.ListTeamsResponse{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return dto.ListTeamsResponse{}, decodeError(res)
	}

	var out dto.ListTeamsResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return dto.ListTeamsResponse{}, err
	}
	return out, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func decodeError(res *http.Response) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err == nil && payload.Error.Message != "" {
		return fmt.Errorf("%s: %s", payload.Error.Code, payload.Error.Message)
	}
	return fmt.Errorf("unexpected status %s", res.Status)
}
