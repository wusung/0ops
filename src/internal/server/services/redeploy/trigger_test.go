package redeploy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/redeploy"
)

func TestTriggerInsertsAndDispatches(t *testing.T) {
	store := newFakeStore()
	disp := &recordingDispatcher{}
	trig := redeploy.NewTrigger(store, disp, recordingSigner{}, "http://localhost:8080")

	result, err := trig.Trigger(context.Background(), redeploy.TriggerArgs{
		TeamID:    "team-1",
		TeamSlug:  "acme",
		AppID:     "app-alpha",
		AppSlug:   "alpha",
		Ref:       "main",
		CommitSHA: "abc123",
		Source:    redeploy.SourceUser,
		TraceID:   "trace-1",
	})
	if err != nil {
		t.Fatalf("Trigger error = %v", err)
	}
	if result.DeployRunID == "" {
		t.Fatalf("missing DeployRunID")
	}
	if len(store.redeployRuns) != 1 || store.redeployRuns[0].Source != "user" {
		t.Fatalf("redeploy run = %+v", store.redeployRuns)
	}
	if len(disp.calls) != 1 || disp.calls[0].RunID != result.DeployRunID {
		t.Fatalf("dispatcher mismatch: %+v", disp.calls)
	}
	if !strings.Contains(disp.calls[0].CallbackURL, "/internal/deploy-runs/"+result.DeployRunID+"/callback") {
		t.Fatalf("callback URL = %s", disp.calls[0].CallbackURL)
	}
}

func TestTriggerWebhookSourceCarriesDeliveryID(t *testing.T) {
	store := newFakeStore()
	disp := &recordingDispatcher{}
	trig := redeploy.NewTrigger(store, disp, recordingSigner{}, "http://localhost:8080")

	_, err := trig.Trigger(context.Background(), redeploy.TriggerArgs{
		TeamID:            "team-1",
		AppID:             "app-alpha",
		AppSlug:           "alpha",
		Ref:               "main",
		CommitSHA:         "abc123",
		Source:            redeploy.SourceWebhook,
		WebhookDeliveryID: "delivery-1",
		TraceID:           "trace-1",
	})
	if err != nil {
		t.Fatalf("Trigger error = %v", err)
	}
	if store.redeployRuns[0].WebhookDeliveryID != "delivery-1" {
		t.Fatalf("delivery id not persisted: %+v", store.redeployRuns[0])
	}
	if store.redeployRuns[0].Source != "webhook" {
		t.Fatalf("source = %q, want webhook", store.redeployRuns[0].Source)
	}
	if store.redeployRuns[0].ActorUserID != "" {
		t.Fatalf("webhook trigger must not carry actor: %q", store.redeployRuns[0].ActorUserID)
	}
}

func TestTriggerWebhookRequiresDeliveryID(t *testing.T) {
	store := newFakeStore()
	trig := redeploy.NewTrigger(store, &recordingDispatcher{}, recordingSigner{}, "")

	_, err := trig.Trigger(context.Background(), redeploy.TriggerArgs{
		TeamID: "team-1",
		AppID:  "app-alpha",
		Source: redeploy.SourceWebhook,
	})
	if err == nil {
		t.Fatalf("expected error when webhook source is missing delivery_id")
	}
}

func TestTriggerWithoutDispatcherSkipsDispatch(t *testing.T) {
	store := newFakeStore()
	trig := redeploy.NewTrigger(store, nil, nil, "")

	result, err := trig.Trigger(context.Background(), redeploy.TriggerArgs{
		TeamID: "team-1",
		AppID:  "app-alpha",
		Source: redeploy.SourceUser,
	})
	if err != nil {
		t.Fatalf("Trigger error = %v", err)
	}
	if result.DeployRunID == "" {
		t.Fatalf("DeployRunID empty")
	}
}

func TestTriggerStoreError(t *testing.T) {
	store := newFakeStore()
	store.insertErr = errors.New("boom")
	trig := redeploy.NewTrigger(store, nil, nil, "")

	_, err := trig.Trigger(context.Background(), redeploy.TriggerArgs{TeamID: "team-1", AppID: "app-alpha", Source: redeploy.SourceUser})
	if err == nil || !strings.Contains(err.Error(), "insert deploy_run") {
		t.Fatalf("unexpected error = %v", err)
	}
}
