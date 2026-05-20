package createapp

import (
	"context"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// fakeEventDispatcher records DispatchEvent calls for assertion in tests.
type fakeEventDispatcher struct {
	lastEventType string
	lastPayload   workflowdispatch.ClientPayload
	err           error
	calls         int
}

func (f *fakeEventDispatcher) DispatchEvent(_ context.Context, eventType string, payload workflowdispatch.ClientPayload) error {
	f.calls++
	f.lastEventType = eventType
	f.lastPayload = payload
	return f.err
}

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

	fake := &fakeEventDispatcher{}
	d := &UploadGHADispatcher{Client: fake}

	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		RunID:    "run-upload-1",
		AppSlug:  "myapp",
		TeamSlug: "acme",
		UploadID: "upl_abc",
	}); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	if fake.lastEventType != uploadGHAEventType {
		t.Fatalf("event_type = %q, want %q", fake.lastEventType, uploadGHAEventType)
	}
	if fake.lastPayload.RunID != "run-upload-1" {
		t.Fatalf("payload.run_id = %q, want run-upload-1", fake.lastPayload.RunID)
	}
	if fake.lastPayload.UploadID != "upl_abc" {
		t.Fatalf("payload.upload_id = %q, want upl_abc", fake.lastPayload.UploadID)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1", fake.calls)
	}
}
