// Package backendclient provides the backend HTTP client.
package backendclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/shared/dto"
)

// Client calls the 0ops backend.
type Client struct {
	BaseURL     string
	BearerToken string
	HTTP        *http.Client

	// RetryMax bounds the number of retry attempts when the server returns
	// 429 Too Many Requests. <= 0 means no retry. Default 5 (set in New).
	RetryMax int
	// RetryBaseDelay is the floor delay used when the server omits
	// Retry-After (and as the seed for exponential backoff). Default 1s
	// (set in New).
	RetryBaseDelay time.Duration
	// NoRetry, when true, disables retry behavior even on 429. Set by
	// CLI `--no-retry` flag or env var `OPS_NO_RETRY=1` (spec § 7.1).
	NoRetry bool
}

// New returns a client with sensible defaults.
func New(baseURL, bearerToken string) *Client {
	return &Client{
		BaseURL:        strings.TrimRight(baseURL, "/"),
		BearerToken:    bearerToken,
		HTTP:           &http.Client{Timeout: 15 * time.Second},
		RetryMax:       5,
		RetryBaseDelay: time.Second,
		NoRetry:        envFlag("OPS_NO_RETRY"),
	}
}

func envFlag(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes"
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

	res, err := c.do(req)
	if err != nil {
		return dto.ListAppsResponse{}, err
	}
	defer func() { _ = res.Body.Close() }()

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

	res, err := c.do(req)
	if err != nil {
		return dto.AppRef{}, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return dto.AppRef{}, decodeError(res)
	}

	var out dto.AppRef
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return dto.AppRef{}, err
	}
	return out, nil
}

// PreviewCreateApp creates a create_app preview.
func (c *Client) PreviewCreateApp(ctx context.Context, teamSlug string, reqBody dto.AppCreateRequest) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/apps:preview"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

// CreateApp confirms a create_app preview.
func (c *Client) CreateApp(ctx context.Context, teamSlug string, reqBody dto.ConfirmCreateAppRequest) (dto.AppCreateResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/apps"
	var out dto.AppCreateResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.AppCreateResponse{}, err
	}
	return out, nil
}

// PreviewRedeploy opens a redeploy preview for the given app.
//
//nolint:revive // exported for public API
func (c *Client) PreviewRedeploy(ctx context.Context, teamSlug, appSlug string, reqBody dto.RedeployRequest) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/apps/" + url.PathEscape(appSlug) + "/redeploys:preview"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

// Redeploy consumes a redeploy preview and triggers the GHA workflow.
//
//nolint:revive // exported for public API
func (c *Client) Redeploy(ctx context.Context, teamSlug string, reqBody dto.ConfirmRedeployRequest) (dto.RedeployResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/redeploys"
	var out dto.RedeployResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.RedeployResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) InspectRepo(ctx context.Context, teamSlug, appSlug string) (dto.RepoInspectResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/repos/" + url.PathEscape(appSlug) + ":inspect"
	var out dto.RepoInspectResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return dto.RepoInspectResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) GetDeployStatus(ctx context.Context, teamSlug, appSlug string) (dto.DeployStatusResponse, error) {
	endpoint, err := url.Parse(c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/deploys/status")
	if err != nil {
		return dto.DeployStatusResponse{}, err
	}
	q := endpoint.Query()
	q.Set("app_slug", appSlug)
	endpoint.RawQuery = q.Encode()

	var out dto.DeployStatusResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint.String(), nil, &out); err != nil {
		return dto.DeployStatusResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) TailLogs(ctx context.Context, teamSlug, appSlug string, limit int) (dto.TailLogsResponse, error) {
	endpoint, err := url.Parse(c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/deploys/logs")
	if err != nil {
		return dto.TailLogsResponse{}, err
	}
	q := endpoint.Query()
	q.Set("app_slug", appSlug)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	endpoint.RawQuery = q.Encode()

	var out dto.TailLogsResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint.String(), nil, &out); err != nil {
		return dto.TailLogsResponse{}, err
	}
	return out, nil
}

// TailLogsFollow streams deploy logs via SSE and returns collected log events.
//
//nolint:revive // exported for public API
func (c *Client) TailLogsFollow(ctx context.Context, teamSlug, appSlug string, limit int, lastEventID string) (dto.TailLogsResponse, error) {
	endpoint, err := url.Parse(c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/deploys/logs")
	if err != nil {
		return dto.TailLogsResponse{}, err
	}
	q := endpoint.Query()
	q.Set("app_slug", appSlug)
	q.Set("follow", "true")
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return dto.TailLogsResponse{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}
	if strings.TrimSpace(lastEventID) != "" {
		req.Header.Set("Last-Event-ID", strings.TrimSpace(lastEventID))
	}

	res, err := c.do(req)
	if err != nil {
		return dto.TailLogsResponse{}, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return dto.TailLogsResponse{}, decodeError(res)
	}
	if !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		return dto.TailLogsResponse{}, fmt.Errorf("unexpected content type %q", res.Header.Get("Content-Type"))
	}

	var (
		out          dto.TailLogsResponse
		currentEvent string
	)
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			currentEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") || currentEvent != "log" {
			continue
		}
		var item dto.LogLine
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &item); err != nil {
			return dto.TailLogsResponse{}, err
		}
		out.Items = append(out.Items, item)
	}
	if err := scanner.Err(); err != nil {
		return dto.TailLogsResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) ListDomains(ctx context.Context, teamSlug, appSlug string) (dto.ListDomainsResponse, error) {
	endpoint, err := url.Parse(c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/domains")
	if err != nil {
		return dto.ListDomainsResponse{}, err
	}
	q := endpoint.Query()
	q.Set("app_slug", appSlug)
	endpoint.RawQuery = q.Encode()

	var out dto.ListDomainsResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint.String(), nil, &out); err != nil {
		return dto.ListDomainsResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) StartDeviceLogin(ctx context.Context, reqBody dto.DeviceStartRequest) (dto.DeviceStartResponse, error) {
	endpoint := c.BaseURL + "/v1/auth/device/start"
	var out dto.DeviceStartResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.DeviceStartResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) PollDeviceLogin(ctx context.Context, reqBody dto.DevicePollRequest) (dto.DevicePollResponse, error) {
	endpoint := c.BaseURL + "/v1/auth/device/poll"
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return dto.DevicePollResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return dto.DevicePollResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}

	res, err := c.do(req)
	if err != nil {
		return dto.DevicePollResponse{}, err
	}
	defer func() { _ = res.Body.Close() }()

	// Handle 202 Accepted - authentication still pending
	if res.StatusCode == http.StatusAccepted {
		return dto.DevicePollResponse{}, fmt.Errorf("authorization_pending")
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return dto.DevicePollResponse{}, decodeError(res)
	}

	var out dto.DevicePollResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return dto.DevicePollResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) CallbackDeviceLogin(ctx context.Context, reqBody dto.DeviceCallbackRequest) (dto.DeviceCallbackResponse, error) {
	endpoint := c.BaseURL + "/v1/auth/device/callback"
	var out dto.DeviceCallbackResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.DeviceCallbackResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) Logout(ctx context.Context) error {
	endpoint := c.BaseURL + "/v1/auth/logout"
	return c.doJSON(ctx, http.MethodPost, endpoint, nil, nil)
}

//nolint:revive // exported for public API
func (c *Client) CreateTeamToken(ctx context.Context, teamSlug string, reqBody dto.PATCreateRequest) (dto.PATCreateResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/tokens/"
	var out dto.PATCreateResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PATCreateResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) ListTeamTokens(ctx context.Context, teamSlug string) (dto.PATListResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/tokens/"
	var out dto.PATListResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return dto.PATListResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) RevokeTeamToken(ctx context.Context, teamSlug, name string) error {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/tokens/" + url.PathEscape(name)
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, nil)
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

	res, err := c.do(req)
	if err != nil {
		return dto.ListTeamsResponse{}, err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return dto.ListTeamsResponse{}, decodeError(res)
	}

	var out dto.ListTeamsResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return dto.ListTeamsResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) BootstrapOwner(ctx context.Context, reqBody dto.BootstrapOwnerRequest) (dto.BootstrapOwnerResponse, error) {
	endpoint := c.BaseURL + "/v1/admin/bootstrap-owner"
	var out dto.BootstrapOwnerResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.BootstrapOwnerResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) ListMembers(ctx context.Context, teamSlug string) (dto.ListMembersResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members"
	var out dto.ListMembersResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return dto.ListMembersResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) PreviewInviteMember(ctx context.Context, teamSlug string, reqBody dto.InviteMemberRequest) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:preview-invite"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) InviteMember(ctx context.Context, teamSlug string, reqBody dto.ConfirmInviteMemberRequest) (dto.InviteMemberResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:invite"
	var out dto.InviteMemberResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.InviteMemberResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) PreviewRemoveMember(ctx context.Context, teamSlug string, reqBody dto.RemoveMemberRequest) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:preview-remove"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, reqBody, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) RemoveMember(ctx context.Context, teamSlug string, reqBody dto.ConfirmRemoveMemberRequest) error {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/members:remove"
	return c.doJSON(ctx, http.MethodPost, endpoint, reqBody, nil)
}

//nolint:revive // exported for public API
func (c *Client) PreviewGitHubInstall(ctx context.Context, teamSlug string) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/github:preview-install"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, nil, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

//nolint:revive // exported for public API
func (c *Client) ConfirmGitHubInstall(ctx context.Context, teamSlug, previewID string) (dto.GitHubInstallResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/github:install"
	var out dto.GitHubInstallResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, dto.GitHubConfirmRequest{PreviewID: previewID}, &out); err != nil {
		return dto.GitHubInstallResponse{}, err
	}
	return out, nil
}

// PreviewGitHubUninstall opens an uninstall_github_app preview row.
//
//nolint:revive // exported for public API
func (c *Client) PreviewGitHubUninstall(ctx context.Context, teamSlug string) (dto.PreviewResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/github:preview-uninstall"
	var out dto.PreviewResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, nil, &out); err != nil {
		return dto.PreviewResponse{}, err
	}
	return out, nil
}

// ConfirmGitHubUninstall consumes the uninstall preview and clears the team's
// installation binding.
//
//nolint:revive // exported for public API
func (c *Client) ConfirmGitHubUninstall(ctx context.Context, teamSlug, previewID string) (dto.GitHubUninstallResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/github:uninstall"
	var out dto.GitHubUninstallResponse
	if err := c.doJSON(ctx, http.MethodPost, endpoint, dto.GitHubConfirmRequest{PreviewID: previewID}, &out); err != nil {
		return dto.GitHubUninstallResponse{}, err
	}
	return out, nil
}

// GetGitHubInstallStatus polls whether the team has an active install binding.
//
//nolint:revive // exported for public API
func (c *Client) GetGitHubInstallStatus(ctx context.Context, teamSlug string) (dto.GitHubInstallStatusResponse, error) {
	endpoint := c.BaseURL + "/v1/teams/" + url.PathEscape(teamSlug) + "/github/install-status"
	var out dto.GitHubInstallStatusResponse
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return dto.GitHubInstallStatusResponse{}, err
	}
	return out, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// do performs req honoring the client's 429 retry policy. On 429 the body
// is drained + closed, and req.Body (if any) is replayed from the
// snapshotted bytes — callers must construct req with a re-readable body
// (the existing code paths use bytes.Reader, which is replay-safe).
//
// Retry schedule: sleep = max(Retry-After header, RetryBaseDelay)
// + jitter (uniform [0, base/5]); RetryBaseDelay doubles between attempts.
// Bounded by RetryMax. Honors req.Context() cancellation between sleeps.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	maxAttempts := c.RetryMax
	if c.NoRetry || maxAttempts < 0 {
		maxAttempts = 0
	}
	bodySnapshot, err := snapshotBody(req)
	if err != nil {
		return nil, err
	}
	base := c.RetryBaseDelay
	if base <= 0 {
		base = time.Second
	}

	for attempt := 0; ; attempt++ {
		if attempt > 0 && bodySnapshot != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodySnapshot))
		}
		res, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusTooManyRequests || attempt >= maxAttempts {
			return res, nil
		}

		retryAfter := parseRetryAfter(res.Header.Get("Retry-After"), base)
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()

		// jitter ±20% (0..base/5)
		jitterCeil := int64(retryAfter / 5)
		if jitterCeil < 1 {
			jitterCeil = 1
		}
		jitter := time.Duration(rand.Int63n(jitterCeil))
		sleep := retryAfter + jitter
		base *= 2

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(sleep):
		}
	}
}

func snapshotBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

func parseRetryAfter(raw string, fallback time.Duration) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return fallback
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

	res, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

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
