// Package slogtrace wraps a slog.Handler so every record emitted with a ctx
// carrying audit.WithTraceID gets a trace_id attribute automatically, without
// requiring each caller to add `slog.With("trace_id", ...)`. Spec § 16 Open
// Questions M2 / ADR-0006 § 4 point 5 follow-up.
//
// Use slog.InfoContext(ctx, msg, args...) (or the *Context variants) at call
// sites to benefit. Plain slog.Info(...) without ctx is unchanged — those
// records are emitted as-is.
package slogtrace

import (
	"context"
	"log/slog"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

// NewHandler returns a slog.Handler that adds a trace_id attribute on every
// record whose ctx carries audit.WithTraceID (and falls back to chi request
// id / 32-zero sentinel via audit.TraceIDFromContext). If inner already
// carries a trace_id attribute via WithAttrs, the inner value wins — we do
// not duplicate.
func NewHandler(inner slog.Handler) slog.Handler {
	return &handler{inner: inner}
}

type handler struct {
	inner slog.Handler
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	traceID := audit.TraceIDFromContext(ctx)
	if traceID != "" && !recordHasAttr(record, "trace_id") {
		record.AddAttrs(slog.String("trace_id", traceID))
	}
	return h.inner.Handle(ctx, record)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(attrs)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name)}
}

// recordHasAttr reports whether record already has an attribute with the
// supplied key, so an explicit caller-supplied trace_id wins over the
// auto-injected ctx value.
func recordHasAttr(record slog.Record, key string) bool {
	var found bool
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}
