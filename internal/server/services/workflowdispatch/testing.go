package workflowdispatch

import "net/http"

// NewClientForTest constructs a Client pointing at an arbitrary base URL.
// Intended for use in tests that need to inspect the HTTP request without
// the env-var machinery of NewClientFromEnv.
func NewClientForTest(apiBaseURL, owner, repo, token string, httpClient *http.Client) *Client {
	return &Client{
		apiBaseURL: apiBaseURL,
		owner:      owner,
		repo:       repo,
		token:      token,
		httpClient: httpClient,
	}
}
