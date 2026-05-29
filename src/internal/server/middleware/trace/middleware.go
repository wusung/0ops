// Package trace exposes the X-Trace-ID resolution + ctx injection HTTP
// middleware used by the production server and any test that exercises the
// full request chain. Keeping a single implementation prevents drift between
// the cmd/server wiring and integration tests (ADR-0006 § 4 point 5).
package trace

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/winshare/zeroops/internal/server/services/audit"
)

// Middleware resolves an inbound trace id from X-Trace-ID → X-Request-ID →
// chi request id → "trace-missing" sentinel, sets the X-Trace-ID response
// header, injects the trace id into the request ctx via audit.WithTraceID
// so downstream handlers can read it with audit.TraceIDFromContext, and
// finally logs the request completion line.
func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
			if traceID == "" {
				traceID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
			}
			if traceID == "" {
				traceID = middleware.GetReqID(r.Context())
			}
			if traceID == "" {
				traceID = "trace-missing"
			}
			w.Header().Set("X-Trace-ID", traceID)

			ctx := audit.WithTraceID(r.Context(), traceID)

			start := time.Now()
			next.ServeHTTP(w, r.WithContext(ctx))
			logger.Info("http request completed",
				"trace_id", traceID,
				"method", r.Method,
				"path", r.URL.Path,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
