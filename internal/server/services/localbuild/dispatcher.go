package localbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// CallbackSender abstracts the HTTP callback so tests can inject a recorder
// without spinning up an httptest server.
type CallbackSender interface {
	Send(ctx context.Context, runID string, ev CallbackEvent) error
}

// AppLookup resolves the local filesystem path + paketo builder for a given
// run. In production this is db-backed (RepoRootLookup); tests use a stub.
type AppLookup interface {
	ResolveLocalPath(ctx context.Context, teamSlug, appSlug string) (path, builder string, err error)
}

// PackFunc is the abstraction over `pack build --publish ... --path ...`.
type PackFunc func(ctx context.Context, imageRef, path, builder string) error

// Dispatcher implements createapp.Dispatcher for file:// repos. Dispatch is
// fire-and-forget (mirrors production GHA workflow_dispatch semantics);
// state is reported back via Callback.
type Dispatcher struct {
	Pack     PackFunc
	Callback CallbackSender
	Lookup   AppLookup
	Registry string

	wg sync.WaitGroup
}

// Dispatch returns immediately; the build runs on a goroutine.
func (d *Dispatcher) Dispatch(_ context.Context, payload workflowdispatch.ClientPayload) error {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		// Detached context: the originating HTTP request may cancel before
		// the build completes; we must finish or the deploy_run is stuck.
		d.run(context.Background(), payload)
	}()
	return nil
}

// WaitForIdle blocks until all dispatched goroutines finish. Test-only.
func (d *Dispatcher) WaitForIdle() { d.wg.Wait() }

func (d *Dispatcher) run(ctx context.Context, payload workflowdispatch.ClientPayload) {
	send := func(ev CallbackEvent) {
		ev.TraceID = payload.TraceID
		ev.OpsToken = payload.OpsToken
		if err := d.Callback.Send(ctx, payload.RunID, ev); err != nil {
			slog.Error("localbuild callback send failed", "run_id", payload.RunID, "status", ev.Status, "err", err.Error())
		}
	}

	slog.Info("localbuild dispatcher start", "run_id", payload.RunID, "image_ref", payload.ImageRef, "team", payload.TeamSlug, "app", payload.AppSlug)
	send(CallbackEvent{Status: "building"})

	var path, builder string
	if d.Lookup != nil {
		p, b, err := d.Lookup.ResolveLocalPath(ctx, payload.TeamSlug, payload.AppSlug)
		if err != nil {
			slog.Error("localbuild resolve local path failed", "run_id", payload.RunID, "err", err.Error())
			send(CallbackEvent{
				Status:                "failed",
				FailureClassification: "build_error",
				ErrorSummary:          truncate(fmt.Sprintf("resolve local path: %v", err), 8192),
			})
			return
		}
		path, builder = p, b
	}
	slog.Info("localbuild resolved path", "run_id", payload.RunID, "path", path, "builder", builder)

	imageRef := payload.ImageRef
	if d.Pack != nil {
		slog.Info("localbuild pack start", "run_id", payload.RunID, "image_ref", imageRef)
		if err := d.Pack(ctx, imageRef, path, builder); err != nil {
			slog.Error("localbuild pack failed", "run_id", payload.RunID, "err", err.Error())
			send(CallbackEvent{
				Status:                "failed",
				FailureClassification: "build_error",
				ErrorSummary:          truncate(err.Error(), 8192),
			})
			return
		}
		slog.Info("localbuild pack ok", "run_id", payload.RunID)
	}

	for _, s := range []string{"pushing", "rendering", "syncing", "live"} {
		ev := CallbackEvent{Status: s}
		if s == "pushing" {
			ev.Image = imageRef
		}
		send(ev)
		time.Sleep(50 * time.Millisecond)
	}
}

// DefaultPack runs `pack build --publish <imageRef> --path <path> --builder <builder>`.
// --publish makes pack push directly, eliminating the build → push race.
func DefaultPack(ctx context.Context, imageRef, path, builder string) error {
	if imageRef == "" || path == "" || builder == "" {
		return errors.New("missing pack arguments")
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "pack", "build", "--publish", imageRef, "--path", path, "--builder", builder)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(buf.String()))
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
