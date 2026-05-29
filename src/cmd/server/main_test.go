package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/winshare/zeroops/internal/server/services/audit"
)

func TestRequestTraceSetsHeader(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil))

	handler := middleware.RequestID(requestTrace(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Trace-ID"); got == "" {
		t.Fatal("expected X-Trace-ID header")
	}
}

func TestRequestTracePrefersIncomingTraceHeader(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil))

	handler := middleware.RequestID(requestTrace(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestRequestTraceInjectsTraceIDIntoContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil))

	var gotFromCtx string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotFromCtx = audit.TraceIDFromContext(r.Context())
	})
	handler := middleware.RequestID(requestTrace(logger)(inner))

	const traceID = "7a9d3c8e4b1f4a2e9c6d5b8a3f0e1d2c"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Trace-ID", traceID)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotFromCtx != traceID {
		t.Fatalf("TraceIDFromContext = %q, want %q", gotFromCtx, traceID)
	}
}

func TestRequestTraceCtxAndHeaderAgreeOnFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(httptest.NewRecorder(), nil))

	var gotFromCtx string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromCtx = audit.TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	handler := middleware.RequestID(requestTrace(logger)(inner))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// no X-Trace-ID — middleware should fall back to chi RequestID, and
	// the same string must appear in BOTH the response header and the ctx.
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
