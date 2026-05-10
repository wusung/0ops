package backendclient

import (
	"bytes"
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

func (c *Client) BootstrapOwner(ctx context.Context, reqBody dto.BootstrapOwnerRequest) (dto.BootstrapOwnerResponse, error) {
	endpoint := c.BaseURL + "/v1/admin/bootstrap-owner"
	var out dto.BootstrapOwnerResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.BootstrapOwnerResponse{}, err
	}
	return out, nil
}

func (c *Client) ListMembers(ctx context.Context, teamSlug string) (dto.ListMembersResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members"
	var out dto.ListMembersResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return dto.ListMembersResponse{}, err
	}
	return out, nil
}

func (c *Client) PreviewInviteMember(ctx context.Context, teamSlug string, reqBody dto.InviteMemberRequest) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:preview-invite"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

func (c *Client) InviteMember(ctx context.Context, teamSlug string, reqBody dto.ConfirmInviteMemberRequest) (dto.InviteMemberResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:invite"
	var out dto.InviteMemberResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.InviteMemberResponse{}, err
	}
	return out, nil
}

func (c *Client) PreviewRemoveMember(ctx context.Context, teamSlug string, reqBody dto.RemoveMemberRequest) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:preview-remove"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

func (c *Client) RemoveMember(ctx context.Context, teamSlug string, reqBody dto.ConfirmRemoveMemberRequest) error {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:remove"
	return c.doJSON(ctx, http.MethodPost, endpoint, reqBody, nil)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, in any, out any) error {
	var bodyReader *bytes.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(payload)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}

	res, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return decodeError(res)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
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
