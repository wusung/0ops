package localbuild

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

type recCallback struct {
	mu     sync.Mutex
	events []CallbackEvent
}

func (r *recCallback) Send(_ context.Context, _ string, ev CallbackEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *recCallback) snapshot() []CallbackEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]CallbackEvent, len(r.events))
	copy(out, r.events)
	return out
}

type stubLookup struct{ path, builder string; err error }

func (s stubLookup) ResolveLocalPath(_ context.Context, _, _ string) (string, string, error) {
	return s.path, s.builder, s.err
}

func TestDispatcherHappyPath(t *testing.T) {
	rec := &recCallback{}
	d := &Dispatcher{
		Pack:     func(_ context.Context, _, _, _ string) error { return nil },
		Callback: rec,
		Lookup:   stubLookup{path: "/workspace/examples/node-demo", builder: "paketobuildpacks/builder-jammy-base"},
	}
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		RunID: "dr_1", AppSlug: "x", TeamSlug: "t", ImageRef: "localhost:5000/0ops-apps/t/x:dr_1",
	}); err != nil {
		t.Fatal(err)
	}
	d.WaitForIdle()

	events := rec.snapshot()
	want := []string{"building", "pushing", "rendering", "syncing", "live"}
	if len(events) != len(want) {
		t.Fatalf("events=%d want %d (%+v)", len(events), len(want), events)
	}
	for i, s := range want {
		if events[i].Status != s {
			t.Errorf("events[%d]=%q want %q", i, events[i].Status, s)
		}
	}
	// "pushing" event must carry the image so the callback handler can
	// persist app.image_ref before reconciler picks it up.
	if events[1].Image != "localhost:5000/0ops-apps/t/x:dr_1" {
		t.Errorf("pushing event missing image: %+v", events[1])
	}
}

func TestDispatcherBuildFailure(t *testing.T) {
	rec := &recCallback{}
	d := &Dispatcher{
		Pack:     func(_ context.Context, _, _, _ string) error { return errors.New("oom") },
		Callback: rec,
		Lookup:   stubLookup{path: "/x", builder: "b"},
	}
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "dr_x"}); err != nil {
		t.Fatal(err)
	}
	d.WaitForIdle()

	events := rec.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %+v", events)
	}
	last := events[len(events)-1]
	if last.Status != "failed" || last.FailureClassification != "build_error" {
		t.Errorf("last event=%+v", last)
	}
	if !strings.Contains(last.ErrorSummary, "oom") {
		t.Errorf("error_summary=%q", last.ErrorSummary)
	}
}

func TestDispatcherLookupFailure(t *testing.T) {
	rec := &recCallback{}
	d := &Dispatcher{
		Pack:     func(_ context.Context, _, _, _ string) error { return nil },
		Callback: rec,
		Lookup:   stubLookup{err: errors.New("repo not found")},
	}
	_ = d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "dr_l"})
	d.WaitForIdle()

	events := rec.snapshot()
	last := events[len(events)-1]
	if last.Status != "failed" || last.FailureClassification != "build_error" {
		t.Errorf("last event=%+v", last)
	}
}

func TestDispatcherIntegratesWithHTTPCallback(t *testing.T) {
	got := make(chan CallbackEvent, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev CallbackEvent
		_ = json.Unmarshal(body, &ev)
		got <- ev
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cc := NewCallbackClient(srv.URL, "k", nil)
	d := &Dispatcher{
		Pack:     func(_ context.Context, _, _, _ string) error { return nil },
		Callback: cc,
		Lookup:   stubLookup{path: "/x", builder: "b"},
	}
	_ = d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "dr_h"})

	// Drain expected 5 events with a generous timeout.
	for i := 0; i < 5; i++ {
		select {
		case ev := <-got:
			if ev.Status == "" {
				t.Fatal("empty status")
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}
}
