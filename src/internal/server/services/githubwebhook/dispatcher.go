package githubwebhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/winshare/zeroops/internal/server/services/githubapp"
)

// GitHubWebhookEventHeader is the GitHub-provided event-type header.
const GitHubWebhookEventHeader = "X-GitHub-Event"

// InstallationHandler is the contract the dispatcher uses to forward
// installation* events. Matches the existing githubapp.Service.HandleInstallationWebhook
// signature so the existing wiring needs no changes.
type InstallationHandler interface {
	HandleInstallationWebhook(ctx context.Context, deliveryID string, payload []byte) (githubapp.WebhookOutcome, error)
}

// DispatcherStore exposes the dedup write reused by push events. The
// installation handler keeps its own internal dedup (see
// githubapp.Service.HandleInstallationWebhook); only push events go
// through the dispatcher-level dedup so we do not double-insert.
type DispatcherStore interface {
	RegisterWebhookDelivery(ctx context.Context, provider, deliveryID string) (bool, error)
}

// Dispatcher resolves a single webhook request to its event-specific
// handler. Handler refs may be nil: missing handlers acknowledge the
// event so GitHub does not retry (spec § 4.2 step 3).
type Dispatcher struct {
	store         DispatcherStore
	verifier      SignatureVerifier
	push          *PushHandler
	installation  InstallationHandler
}

// NewDispatcher wires the dispatcher.
func NewDispatcher(store DispatcherStore, verifier SignatureVerifier, push *PushHandler, installation InstallationHandler) *Dispatcher {
	return &Dispatcher{store: store, verifier: verifier, push: push, installation: installation}
}

// DispatchResult summarises the dispatch outcome. Status mirrors the
// `dispatch_status` audit field for easy log filtering.
type DispatchResult struct {
	Status            string
	Event             string
	DeliveryID        string
	Push              *PushOutcome
	Installation      *githubapp.WebhookOutcome
}

// Error classes returned by Dispatch; the HTTP handler maps these to
// apperror codes (spec § 9 error-model alignment).
type (
	// ErrPayloadTooLarge mirrors spec § 9 `webhook_payload_too_large`.
	ErrPayloadTooLarge struct{}
	// ErrSignatureInvalid mirrors spec § 9 `webhook_signature_invalid`.
	ErrSignatureInvalid struct{ Reason string }
	// ErrMissingDeliveryID is returned when the X-GitHub-Delivery header is empty.
	ErrMissingDeliveryID struct{}
	// ErrUnconfigured is returned when no signature verifier is wired.
	ErrUnconfigured struct{}
)

func (ErrPayloadTooLarge) Error() string  { return "github webhook payload too large" }
func (e ErrSignatureInvalid) Error() string {
	if e.Reason == "" {
		return "github webhook signature invalid"
	}
	return "github webhook signature invalid: " + e.Reason
}
func (ErrMissingDeliveryID) Error() string { return "github webhook missing X-GitHub-Delivery" }
func (ErrUnconfigured) Error() string      { return "github webhook verifier not configured" }

// Dispatch verifies the request, dedups by delivery id, then routes to the
// event handler. Order matters: payload-size check → signature → dedup →
// route. (spec § 4.2 + § 14 hard rule #10).
func (d *Dispatcher) Dispatch(ctx context.Context, r *http.Request) (DispatchResult, error) {
	if d.verifier == nil {
		return DispatchResult{}, ErrUnconfigured{}
	}

	// Step 1: bounded body read. This rebinds r.Body so the verifier can
	// re-read.
	if _, err := ReadBoundedBody(r); err != nil {
		var tooLarge PayloadTooLargeError
		if errors.As(err, &tooLarge) {
			return DispatchResult{}, ErrPayloadTooLarge{}
		}
		return DispatchResult{}, fmt.Errorf("read body: %w", err)
	}

	// Step 2: signature.
	body, err := d.verifier.VerifyRequest(r)
	if err != nil {
		return DispatchResult{}, ErrSignatureInvalid{Reason: err.Error()}
	}

	// Step 3: event-type whitelist (ack-and-drop unsupported events).
	event := strings.TrimSpace(r.Header.Get(GitHubWebhookEventHeader))
	res := DispatchResult{Event: event}
	if !IsAcknowledgedEvent(event) {
		res.Status = "ignored_event"
		return res, nil
	}
	if event == EventPing {
		res.Status = "pong"
		return res, nil
	}

	// Step 4: delivery_id (every non-ping event must carry one).
	deliveryID := strings.TrimSpace(r.Header.Get(githubapp.GitHubWebhookDeliveryHeader))
	if deliveryID == "" {
		return res, ErrMissingDeliveryID{}
	}
	res.DeliveryID = deliveryID

	// Step 5: route. Dedup is delegated to each handler — push handler
	// dedups via webhook_dedup at entry; installation handler keeps the
	// dedup it already owned pre-refactor. Sharing the same provider key
	// across both keeps spec § 4.3 "24h唯一" guarantee intact without the
	// dispatcher double-inserting (see TestGitHubWebhookDuplicateDeliveryShortCircuit).
	switch event {
	case EventPush:
		if d.push == nil {
			res.Status = "no_push_handler"
			return res, nil
		}
		inserted, err := d.store.RegisterWebhookDelivery(ctx, "github", deliveryID)
		if err != nil {
			return res, fmt.Errorf("register delivery: %w", err)
		}
		if !inserted {
			res.Status = "duplicate"
			return res, nil
		}
		traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
		outcome, err := d.push.Handle(ctx, deliveryID, traceID, body)
		if err != nil {
			return res, fmt.Errorf("push handler: %w", err)
		}
		res.Push = &outcome
		if outcome.Acted {
			res.Status = "triggered"
		} else {
			res.Status = "skipped"
		}
		return res, nil

	case EventInstallation, EventInstallationRepositories:
		if d.installation == nil {
			res.Status = "no_installation_handler"
			return res, nil
		}
		outcome, err := d.installation.HandleInstallationWebhook(ctx, deliveryID, body)
		if err != nil {
			return res, fmt.Errorf("installation handler: %w", err)
		}
		res.Installation = &outcome
		if outcome.Duplicate {
			res.Status = "duplicate"
		} else if outcome.Acted {
			res.Status = "applied"
		} else {
			res.Status = "noop"
		}
		return res, nil
	}

	res.Status = "ignored_event"
	return res, nil
}

// EncodeResponse writes a JSON status envelope mirroring the existing
// install-only handler so consumers (incl. test harness) need no schema
// changes.
func EncodeResponse(w http.ResponseWriter, res DispatchResult) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"status":      res.Status,
		"event":       res.Event,
		"delivery_id": res.DeliveryID,
	}
	if res.Push != nil {
		payload["team_slug"] = res.Push.TeamSlug
		payload["triggered"] = res.Push.Triggered
		payload["skipped"] = res.Push.Skipped
		payload["acted"] = res.Push.Acted
		if res.Push.Reason != "" {
			payload["reason"] = res.Push.Reason
		}
	}
	if res.Installation != nil {
		payload["team_slug"] = res.Installation.TeamSlug
		payload["acted"] = res.Installation.Acted
		payload["duplicate"] = res.Installation.Duplicate
		payload["paused"] = res.Installation.PausedAppCount
		payload["event_type"] = res.Installation.Action
	}
	_ = json.NewEncoder(w).Encode(payload)
}

