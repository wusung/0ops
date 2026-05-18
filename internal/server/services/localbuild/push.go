package localbuild

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// PushFunc is the abstraction over `docker push` against the mounted podman
// socket. Separated from PackFunc so tests can inject a recorder without
// spinning up an actual unix socket server.
type PushFunc func(ctx context.Context, imageRef string) error

// DefaultPush pushes imageRef via the host podman daemon's Docker-compat
// REST API. Socket path is taken from DOCKER_HOST (compose sets this so
// server-container and host see the same path; required for pack to mount
// the socket into its lifecycle container). Falls back to the rootless
// default if DOCKER_HOST is unset. Per ADR-0012 M5.6.1.
func DefaultPush(ctx context.Context, imageRef string) error {
	socketPath := dockerHostSocketPath()
	return PushViaSocket(ctx, socketPath, imageRef)
}

func dockerHostSocketPath() string {
	raw := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	if raw == "" {
		return defaultRootlessSocket()
	}
	if strings.HasPrefix(raw, "unix://") {
		return strings.TrimPrefix(raw, "unix://")
	}
	return raw
}

func defaultRootlessSocket() string {
	if uid := os.Getuid(); uid > 0 {
		return fmt.Sprintf("/run/user/%d/podman/podman.sock", uid)
	}
	return "/run/podman/podman.sock"
}

// PushViaSocket is the underlying implementation; exposed so tests can dial
// an httptest server-on-unix-socket.
func PushViaSocket(ctx context.Context, socketPath, imageRef string) error {
	if strings.TrimSpace(imageRef) == "" {
		return fmt.Errorf("missing imageRef")
	}
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Minute}

	// Docker-compat API expects image name in the URL path (not tag in query
	// for podman; podman accepts both forms).
	endpoint := fmt.Sprintf("http://d/v1.40/images/%s/push", url.PathEscape(imageRef))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	// Anonymous auth: registry is local and insecure; podman default policy
	// treats localhost as insecure-no-auth.
	req.Header.Set("X-Registry-Auth", "e30=") // base64("{}")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("podman push request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("podman push %s: status %d", imageRef, resp.StatusCode)
	}
	// Response is a chunked stream of JSON status events; the call only
	// succeeds when no event carries an "error" / "errorDetail" key.
	scanner := bufio.NewScanner(resp.Body)
	// Push event lines can exceed default 64 KiB on dense progress reports.
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Error       string `json:"error,omitempty"`
			ErrorDetail struct {
				Message string `json:"message,omitempty"`
			} `json:"errorDetail,omitempty"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			// Non-JSON line is ignored; podman occasionally emits plain
			// status text before/after the JSON stream.
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("podman push %s: %s", imageRef, ev.Error)
		}
		if ev.ErrorDetail.Message != "" {
			return fmt.Errorf("podman push %s: %s", imageRef, ev.ErrorDetail.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("podman push stream: %w", err)
	}
	return nil
}
