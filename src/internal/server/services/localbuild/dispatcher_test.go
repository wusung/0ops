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
	var packedRef, pushedRef string
	d := &Dispatcher{
		Pack: func(_ context.Context, ref, _, _ string) error {
			packedRef = ref
			return nil
		},
		Push: func(_ context.Context, ref string) error {
			pushedRef = ref
			return nil
		},
		Callback: rec,
		Lookup:   stubLookup{path: "/workspace/examples/node-demo", builder: "paketobuildpacks/builder-jammy-base"},
		Registry: "localhost:5000",
	}
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		RunID: "dr_1", AppSlug: "x", TeamSlug: "t",
		// orchestrator hard-codes ghcr.io; dispatcher must rewrite to LOCAL_REGISTRY.
		ImageRef: "ghcr.io/winshare/0ops-apps/t/x:dr_1",
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
	wantRef := "localhost:5000/0ops-apps/t/x:dr_1"
	if packedRef != wantRef {
		t.Errorf("pack got ref %q want %q", packedRef, wantRef)
	}
	if pushedRef != wantRef {
		t.Errorf("push got ref %q want %q", pushedRef, wantRef)
	}
	if events[1].Image != wantRef {
		t.Errorf("pushing event missing rewritten image: %+v", events[1])
	}
}

func TestDispatcherPushFailure(t *testing.T) {
	rec := &recCallback{}
	d := &Dispatcher{
		Pack:     func(_ context.Context, _, _, _ string) error { return nil },
		Push:     func(_ context.Context, _ string) error { return errors.New("registry refused: unauthorized") },
		Callback: rec,
		Lookup:   stubLookup{path: "/x", builder: "b"},
		Registry: "localhost:5000",
	}
	_ = d.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		RunID: "dr_p", ImageRef: "ghcr.io/winshare/0ops-apps/t/x:dr_p",
	})
	d.WaitForIdle()

	events := rec.snapshot()
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (building/pushing/failed), got %+v", events)
	}
	last := events[len(events)-1]
	if last.Status != "failed" || last.FailureClassification != "registry_push_failed" {
		t.Errorf("last event=%+v", last)
	}
	if !strings.Contains(last.ErrorSummary, "unauthorized") {
		t.Errorf("error_summary=%q", last.ErrorSummary)
	}
}

func TestRewriteImageRef(t *testing.T) {
	cases := []struct {
		in       string
		registry string
		want     string
	}{
		{"ghcr.io/winshare/0ops-apps/t/x:dr_1", "localhost:5000", "localhost:5000/0ops-apps/t/x:dr_1"},
		{"ghcr.io/other/foo:1", "localhost:5000", "localhost:5000/other/foo:1"},
		{"unrelated/foo:1", "localhost:5000", "unrelated/foo:1"},
		{"ghcr.io/winshare/x:1", "", "ghcr.io/winshare/x:1"},
	}
	for _, tc := range cases {
		got := rewriteImageRef(tc.in, tc.registry)
		if got != tc.want {
			t.Errorf("rewriteImageRef(%q, %q) = %q, want %q", tc.in, tc.registry, got, tc.want)
		}
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
	if last.Status != "failed" || last.FailureClassification != "build_compile_error" {
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
	if last.Status != "failed" || last.FailureClassification != "build_compile_error" {
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
