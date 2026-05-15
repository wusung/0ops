package reconciler

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

const (
	// BaseBackoff is the spec § 16 #4 floor: 60s × 2^attempts capped
	// at MaxBackoff. The reconciler MUST NOT replace it on a per-kind
	// basis (hard rule #4).
	BaseBackoff = 60 * time.Second
	// MaxBackoff caps the exponential backoff at 30 min so failed jobs
	// keep ticking without flooding the worker queue.
	MaxBackoff = 30 * time.Minute
	// MaxAttempts is the spec § 16 #4 limit: > 8 attempts → permanently
	// failed and an incident opens automatically.
	MaxAttempts = 8
)

// ErrUnknownKind is returned by JobHandlers when an enqueued job uses
// a kind the worker pool has not registered. The runner records the
// error and reschedules the job; the on-call rotation reviews repeats
// via dashboards.
var ErrUnknownKind = errors.New("reconciler: unknown job kind")

// HandlerOutcome captures the result of one job handler invocation.
// Completed=true terminates the row (success); FailedPermanently=true
// flips it to failed_permanently and triggers an incident in spec
// § 9.2. Otherwise the runner reschedules with the spec backoff.
type HandlerOutcome struct {
	Completed         bool
	FailedPermanently bool
	LastError         string
}

// Handler is the per-kind worker contract. Implementations must be
// idempotent against retries — the same job may fire multiple times
// before a Completed/FailedPermanently outcome is returned.
type Handler interface {
	Handle(ctx context.Context, job db.ReconciliationJobRow) HandlerOutcome
}

// HandlerFunc adapts a plain function into a Handler.
type HandlerFunc func(ctx context.Context, job db.ReconciliationJobRow) HandlerOutcome

// Handle invokes the underlying function.
func (f HandlerFunc) Handle(ctx context.Context, job db.ReconciliationJobRow) HandlerOutcome {
	return f(ctx, job)
}

// HandlerRegistry maps reconciliation_job.kind values onto their
// per-kind handlers. The runner enforces the leader gate before
// dispatching; handlers must not assume leadership themselves.
type HandlerRegistry struct {
	handlers map[string]Handler
}

// NewHandlerRegistry returns an empty registry ready for Register calls.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]Handler)}
}

// Register binds a handler to a kind. Duplicate registrations panic so
// startup configuration mistakes surface immediately.
func (r *HandlerRegistry) Register(kind string, h Handler) {
	if _, ok := r.handlers[kind]; ok {
		panic("reconciler: duplicate handler for kind " + kind)
	}
	r.handlers[kind] = h
}

// Lookup returns the handler bound to a kind. The boolean is false
// when no handler is registered; the runner translates a miss into
// a reschedule with ErrUnknownKind so the on-call rotation notices.
func (r *HandlerRegistry) Lookup(kind string) (Handler, bool) {
	h, ok := r.handlers[kind]
	return h, ok
}

// NextBackoff computes the spec § 16 #4 retry delay for the next
// attempt: min(60s × 2^attempts, 30 min). attempts is the number of
// failures already recorded — pass job.Attempts before incrementing.
func NextBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	mult := math.Pow(2, float64(attempts))
	delay := time.Duration(float64(BaseBackoff) * mult)
	if delay > MaxBackoff {
		return MaxBackoff
	}
	return delay
}

// ShouldFailPermanently reports whether the current attempts count is
// past the spec § 16 #4 retry cap. The caller still increments before
// reporting, so the comparison checks against MaxAttempts (8).
func ShouldFailPermanently(attempts int) bool {
	return attempts > MaxAttempts
}
