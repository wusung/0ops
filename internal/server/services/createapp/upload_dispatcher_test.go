package createapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// TestUploadGHADispatcher_NilClientIsNoOp verifies that a nil Client is
// safely ignored (no panic, no error).
func TestUploadGHADispatcher_NilClientIsNoOp(t *testing.T) {
	t.Parallel()

	d := &UploadGHADispatcher{Client: nil}
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "r1"}); err != nil {
		t.Fatalf("expected nil error for nil client, got %v", err)
	}
}

// TestUploadGHADispatcher_NilReceiverIsNoOp verifies that a nil *UploadGHADispatcher
// is also safe.
func TestUploadGHADispatcher_NilReceiverIsNoOp(t *testing.T) {
	t.Parallel()

	var d *UploadGHADispatcher
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "r1"}); err != nil {
		t.Fatalf("expected nil error for nil receiver, got %v", err)
	}
}

// TestUploadGHADispatcher_OverridesEventType verifies that UploadGHADispatcher
// posts event_type="deploy-app-from-upload" rather than the default "deploy-app".
func TestUploadGHADispatcher_OverridesEventType(t *testing.T) {
	t.Parallel()

	var capturedBody struct {
		EventType string                        `json:"event_type"`
		Payload   workflowdispatch.ClientPayload `json:"client_payload"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &capturedBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// Construct a client that points at the test server.
	client := workflowdispatch.NewClientForTest(srv.URL, "winshare", "0ops", "test-token", srv.Client())
	d := &UploadGHADispatcher{Client: client}

	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		RunID:    "run-upload-1",
		AppSlug:  "myapp",
		TeamSlug: "acme",
		UploadID: "upl_abc",
	}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	if capturedBody.EventType != uploadGHAEventType {
		t.Fatalf("event_type = %q, want %q", capturedBody.EventType, uploadGHAEventType)
	}
	if capturedBody.Payload.RunID != "run-upload-1" {
		t.Fatalf("payload.run_id = %q, want run-upload-1", capturedBody.Payload.RunID)
	}
	if capturedBody.Payload.UploadID != "upl_abc" {
		t.Fatalf("payload.upload_id = %q, want upl_abc", capturedBody.Payload.UploadID)
	}
}
