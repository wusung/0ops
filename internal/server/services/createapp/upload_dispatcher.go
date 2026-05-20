package createapp

import (
	"context"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// uploadGHAEventType is the GHA repository_dispatch event_type for the
// upload-source build workflow (T15: deploy-app-from-upload.yml).
const uploadGHAEventType = "deploy-app-from-upload"

// eventDispatcher abstracts the GHA repository_dispatch call so
// UploadGHADispatcher can be tested without a real *workflowdispatch.Client.
// Production: *workflowdispatch.Client satisfies this via its DispatchEvent method.
type eventDispatcher interface {
	DispatchEvent(ctx context.Context, eventType string, payload workflowdispatch.ClientPayload) error
}

// UploadGHADispatcher dispatches an upload-source create_app to the GHA
// workflow that fetches the tarball from /v1/uploads/{id}/archive (T9).
// Satisfies the createapp.Dispatcher interface and is intended to be wired
// alongside the existing GitHub (ADR-0005) and Local (ADR-0012) dispatchers
// through RoutingDispatcher.
type UploadGHADispatcher struct {
	Client eventDispatcher
}

// Dispatch triggers the "deploy-app-from-upload" repository_dispatch event.
// Nil-tolerant: if the receiver or its Client is nil, the call is a no-op.
// This matches the existing GitHub dispatcher nil-tolerance pattern.
func (u *UploadGHADispatcher) Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error {
	if u == nil || u.Client == nil {
		return nil
	}
	return u.Client.DispatchEvent(ctx, uploadGHAEventType, payload)
}
