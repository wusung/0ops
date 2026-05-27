package githubwebhook

import (
	"encoding/json"
	"strings"
)

// Event types recognised by the dispatcher (spec § 4.2 step 3 whitelist).
const (
	EventPush                    = "push"
	EventInstallation            = "installation"
	EventInstallationRepositories = "installation_repositories"
	EventPing                    = "ping"
)

// PushPayload is the subset of GitHub `push` webhook fields the handler
// reads (spec § 5.1). All other fields are ignored.
type PushPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		ID            int64  `json:"id"`
		HTMLURL       string `json:"html_url"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// ParsePushPayload decodes the JSON body into a PushPayload.
func ParsePushPayload(body []byte) (PushPayload, error) {
	var out PushPayload
	if err := json.Unmarshal(body, &out); err != nil {
		return PushPayload{}, err
	}
	return out, nil
}

// BranchFromRef converts a GitHub ref ("refs/heads/main", "refs/tags/v1")
// into ("main", true) for heads and ("", false) for anything else (spec
// § 5.2 step 2 — tag pushes are ignored in v1).
func BranchFromRef(ref string) (string, bool) {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	branch := strings.TrimPrefix(ref, prefix)
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", false
	}
	return branch, true
}

// NormalizeRepoURL strips trailing slashes and `.git` suffix so the value
// matches what app.repo_url stores (spec § 5.2 step 4).
func NormalizeRepoURL(raw string) string {
	out := strings.TrimSpace(raw)
	out = strings.TrimRight(out, "/")
	out = strings.TrimSuffix(out, ".git")
	return out
}

// IsAcknowledgedEvent reports whether the event type is in the spec § 4.2
// step 3 whitelist (push / installation* / ping). Any other event must be
// 200 OK + ignored so GitHub does not retry indefinitely.
func IsAcknowledgedEvent(eventType string) bool {
	switch eventType {
	case EventPush, EventInstallation, EventInstallationRepositories, EventPing:
		return true
	default:
		return false
	}
}
