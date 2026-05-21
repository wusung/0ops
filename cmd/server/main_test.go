package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
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
