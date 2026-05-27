package workflowdispatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDispatchPostsRepositoryDispatch(t *testing.T) {
	t.Parallel()

	var captured struct {
		EventType string        `json:"event_type"`
		Payload   ClientPayload `json:"client_payload"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.URL.Path; got != "/repos/winshare/0ops/dispatches" {
			t.Fatalf("path = %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client := &Client{
		apiBaseURL: srv.URL,
		owner:      "winshare",
		repo:       "0ops",
		token:      "secret-token",
		httpClient: srv.Client(),
	}
	if err := client.Dispatch(context.Background(), ClientPayload{
		RunID:       "run-1",
		AppSlug:     "nextdemo",
		TeamSlug:    "acme",
		Ref:         "main",
		CallbackURL: "https://ops.example/internal/deploy-runs/run-1/callback",
		TraceID:     "trace-1",
	}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	if captured.EventType != "deploy-app" {
		t.Fatalf("event_type = %q", captured.EventType)
	}
	if captured.Payload.RunID != "run-1" || captured.Payload.TeamSlug != "acme" {
		t.Fatalf("payload = %#v", captured.Payload)
	}
}

// TestClient_DispatchUsesDefaultEventType verifies that Dispatch() always
// sends event_type="deploy-app" (backward compat guarantee after T14 refactor).
func TestClient_DispatchUsesDefaultEventType(t *testing.T) {
	t.Parallel()

	var capturedEventType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EventType string `json:"event_type"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		capturedEventType = body.EventType
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client := &Client{
		apiBaseURL: srv.URL,
		owner:      "o",
		repo:       "r",
		token:      "t",
		httpClient: srv.Client(),
	}
	if err := client.Dispatch(context.Background(), ClientPayload{RunID: "r1"}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if capturedEventType != "deploy-app" {
		t.Fatalf("event_type = %q, want deploy-app", capturedEventType)
	}
}

// TestClient_DispatchEventUsesCustomEventType verifies that DispatchEvent()
// sends the caller-supplied event_type.
func TestClient_DispatchEventUsesCustomEventType(t *testing.T) {
	t.Parallel()

	var capturedEventType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EventType string `json:"event_type"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		capturedEventType = body.EventType
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	client := &Client{
		apiBaseURL: srv.URL,
		owner:      "o",
		repo:       "r",
		token:      "t",
		httpClient: srv.Client(),
	}
	if err := client.DispatchEvent(context.Background(), "deploy-app-from-upload", ClientPayload{RunID: "r1"}); err != nil {
		t.Fatalf("DispatchEvent() error = %v", err)
	}
	if capturedEventType != "deploy-app-from-upload" {
		t.Fatalf("event_type = %q, want deploy-app-from-upload", capturedEventType)
	}
}
