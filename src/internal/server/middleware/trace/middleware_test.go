package trace_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"

	tracemw "github.com/winshare/zeroops/internal/server/middleware/trace"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

func discardLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMiddlewareSetsHeader(t *testing.T) {
	handler := chimw.RequestID(tracemw.Middleware(discardLogger(t))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Trace-ID"); got == "" {
		t.Fatal("expected X-Trace-ID header")
	}
}

func TestMiddlewarePrefersIncomingTraceHeader(t *testing.T) {
	handler := chimw.RequestID(tracemw.Middleware(discardLogger(t))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Trace-ID", "trace-from-caller")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Trace-ID"); got != "trace-from-caller" {
		t.Fatalf("X-Trace-ID = %q, want trace-from-caller", got)
	}
}

func TestMiddlewareInjectsTraceIDIntoContext(t *testing.T) {
	var gotFromCtx string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotFromCtx = audit.TraceIDFromContext(r.Context())
	})
	handler := chimw.RequestID(tracemw.Middleware(discardLogger(t))(inner))

	const traceID = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Trace-ID", traceID)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotFromCtx != traceID {
		t.Fatalf("TraceIDFromContext = %q, want %q", gotFromCtx, traceID)
	}
}

func TestMiddlewareCtxAndHeaderAgreeOnFallback(t *testing.T) {
	var gotFromCtx string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromCtx = audit.TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	handler := chimw.RequestID(tracemw.Middleware(discardLogger(t))(inner))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fromHeader := rec.Header().Get("X-Trace-ID")
	if fromHeader == "" {
		t.Fatalf("expected response header X-Trace-ID to be set")
	}
	if gotFromCtx != fromHeader {
		t.Fatalf("ctx trace = %q, header trace = %q — must agree", gotFromCtx, fromHeader)
	}
}
