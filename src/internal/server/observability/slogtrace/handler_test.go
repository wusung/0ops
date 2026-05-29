package slogtrace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/server/observability/slogtrace"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

func newLogger(buf *bytes.Buffer) *slog.Logger {
	inner := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(slogtrace.NewHandler(inner))
}

func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("no log line emitted")
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(line), &row); err != nil {
		t.Fatalf("unmarshal log line: %v (raw=%q)", err, line)
	}
	return row
}

func TestHandlerInjectsTraceIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	const traceID = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
	ctx := audit.WithTraceID(context.Background(), traceID)

	logger.InfoContext(ctx, "hello", "key", "value")

	row := decodeOne(t, &buf)
	if got, _ := row["trace_id"].(string); got != traceID {
		t.Fatalf("trace_id = %q, want %q", got, traceID)
	}
	if got, _ := row["key"].(string); got != "value" {
		t.Fatalf("key = %q, want value", got)
	}
}

func TestHandlerWithoutContextInjectsSentinel(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	logger.InfoContext(context.Background(), "noop")

	row := decodeOne(t, &buf)
	const sentinel = "00000000000000000000000000000000"
	if got, _ := row["trace_id"].(string); got != sentinel {
		t.Fatalf("trace_id = %q, want sentinel %q", got, sentinel)
	}
}

func TestHandlerExplicitTraceIDWins(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	const ctxTrace = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
	const explicit = "explicit-override"
	ctx := audit.WithTraceID(context.Background(), ctxTrace)

	logger.InfoContext(ctx, "hello", "trace_id", explicit)

	row := decodeOne(t, &buf)
	if got, _ := row["trace_id"].(string); got != explicit {
		t.Fatalf("trace_id = %q, want explicit %q (caller value must beat auto-inject)", got, explicit)
	}
}

func TestHandlerNonContextSlogStillEmits(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	// Without ctx the package routes through context.Background, so
	// TraceIDFromContext falls back to the 32-zero sentinel; the log line is
	// still emitted (no error path) and the sentinel surfaces.
	logger.Info("legacy", "key", "value")

	row := decodeOne(t, &buf)
	if got, _ := row["trace_id"].(string); got == "" {
		t.Fatalf("expected sentinel trace_id, got empty")
	}
	if got, _ := row["key"].(string); got != "value" {
		t.Fatalf("key = %q, want value", got)
	}
}
