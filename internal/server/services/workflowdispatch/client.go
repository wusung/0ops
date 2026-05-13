package workflowdispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/services/githubapp"
)

var ErrMissingRepository = errors.New("missing GitHub repository")

// ClientPayload is sent to GitHub repository_dispatch.
type ClientPayload struct {
	RunID       string `json:"run_id"`
	AppSlug     string `json:"app_slug"`
	TeamSlug    string `json:"team_slug"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	Ref         string `json:"ref"`
	ImageRef    string `json:"image_ref,omitempty"`
	OpsToken    string `json:"ops_token,omitempty"`
	CallbackURL string `json:"callback_url"`
	TraceID     string `json:"trace_id"`
}

// Client sends repository_dispatch events to the 0ops repo.
type Client struct {
	apiBaseURL string
	owner      string
	repo       string
	token      string
	httpClient *http.Client
}

// NewClientFromEnv builds a dispatch client from environment variables.
func NewClientFromEnv(httpClient *http.Client) (*Client, error) {
	owner, repo, err := repositoryFromEnv()
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(os.Getenv("OPS_GITHUB_API_TOKEN"))
	if token == "" {
		signer, err := githubapp.NewJWTSignerFromEnv()
		if err != nil {
			return nil, err
		}
		installRaw := strings.TrimSpace(os.Getenv("OPS_GITHUB_APP_INSTALLATION_ID"))
		if installRaw == "" {
			return nil, fmt.Errorf("missing OPS_GITHUB_API_TOKEN or OPS_GITHUB_APP_INSTALLATION_ID")
		}
		installID, err := parseInt64(installRaw)
		if err != nil {
			return nil, fmt.Errorf("parse OPS_GITHUB_APP_INSTALLATION_ID: %w", err)
		}
		installClient := githubapp.NewInstallationTokenClient(signer, githubAPIBaseURL(), httpClient)
		tokenResp, err := installClient.GetAccessToken(context.Background(), installID)
		if err != nil {
			return nil, err
		}
		token = tokenResp.Token
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		apiBaseURL: githubAPIBaseURL(),
		owner:      owner,
		repo:       repo,
		token:      token,
		httpClient: httpClient,
	}, nil
}

// Dispatch triggers repository_dispatch with event_type deploy-app.
func (c *Client) Dispatch(ctx context.Context, payload ClientPayload) error {
	if c == nil {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"event_type":     "deploy-app",
		"client_payload": payload,
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/dispatches", c.apiBaseURL, c.owner, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "0ops-server")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github dispatch %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

func repositoryFromEnv() (string, string, error) {
	repo := strings.TrimSpace(os.Getenv("OPS_GITHUB_REPOSITORY"))
	if repo != "" {
		parts := strings.SplitN(repo, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	owner := strings.TrimSpace(os.Getenv("OPS_GITHUB_REPOSITORY_OWNER"))
	name := strings.TrimSpace(os.Getenv("OPS_GITHUB_REPOSITORY_NAME"))
	if owner == "" || name == "" {
		return "", "", ErrMissingRepository
	}
	return owner, name, nil
}

func githubAPIBaseURL() string {
	base := strings.TrimSpace(os.Getenv("OPS_GITHUB_API_BASE_URL"))
	if base == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(base, "/")
}

func parseInt64(raw string) (int64, error) {
	var out int64
	_, err := fmt.Sscan(raw, &out)
	return out, err
}
