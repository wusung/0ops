// Package main provides the 0ops HTTP server entrypoint.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	appserver "github.com/winshare/zeroops/internal/server"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/health"
	ratelimit "github.com/winshare/zeroops/internal/server/middleware/ratelimit"
	"github.com/winshare/zeroops/internal/server/observability"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/server/services/cloudflare"
	"github.com/winshare/zeroops/internal/server/services/k3s"
	"github.com/winshare/zeroops/internal/shared"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	slog.SetDefault(logger)

	addr := envOr("OPS_LISTEN_ADDR", ":8080")
	metrics := observability.NewMetrics()
	pool, err := db.NewPool(context.Background(), db.ConfigFromEnv())
	if err != nil {
		logger.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	repo := db.NewRepository(pool)
	appserver.BindCreateAppMetrics(metrics.ObserveCreateAppPreview, metrics.ObserveCreateAppConfirm)
	appserver.BindPlatformMetrics(
		metrics.ObservePreviewCreated,
		metrics.ObservePreviewConsumed,
		metrics.ObserveDeployRunTerminal,
		metrics.ObserveDeployRunLeadTime,
		metrics.ObserveDeployRunFailure,
	)
	cloudflare.BindMetrics(metrics.ObserveCloudflareAPICall)
	cloudflare.BindCallDurationMetric(metrics.ObserveCloudflareAPICallDuration)

	// Initialize K3s and Cloudflare infrastructure clients
	k3sCfg := &k3s.Config{
		KubeconfigPath:            envOr("K3S_KUBECONFIG_PATH", ""),
		APIServerURL:              envOr("K3S_API_SERVER_URL", ""),
		DisableNamespaceIsolation: envOr("K3S_DISABLE_ISOLATION", "") == "true",
	}
	k3sClient, err := k3s.NewClient(k3sCfg)
	if err != nil {
		logger.Error("failed to initialize K3s client", "err", err)
		os.Exit(1)
	}

	cfCfg := &cloudflare.Config{
		TunnelID:               envOr("CF_TUNNEL_ID", ""),
		APIToken:               envOr("CF_API_TOKEN", ""),
		AccountID:              envOr("CF_ACCOUNT_ID", ""),
		ZoneID:                 envOr("CF_ZONE_ID", ""),
		DisableTunnelIsolation: envOr("CF_DISABLE_TUNNEL", "") == "true",
	}
	cfClient, err := cloudflare.NewClient(cfCfg)
	if err != nil {
		logger.Error("failed to initialize Cloudflare client", "err", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestTrace(logger))
	r.Use(middleware.Recoverer)
	r.Use(metrics.Middleware(routeLabel))

	r.Get("/health", health.Handler())
	r.Method(http.MethodGet, "/metrics", metrics.Handler())

	limiter := ratelimit.New(ratelimit.Config{Quotas: ratelimit.DefaultPlanQuotas()})
	auditSvc := audit.NewService(repo, repo, audit.NopObserver())
	r.Mount("/", appserver.NewRouterWithRateLimitAndAudit(repo, k3sClient, cfClient, limiter, metrics.RateLimitObserver(), auditSvc))

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go limiter.RunCleanup(ctx, time.Hour)

	go func() {
		logger.Info("0ops-server listening", "addr", addr, "version", shared.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server exited unexpectedly", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown initiated")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
}

func requestTrace(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := middleware.GetReqID(r.Context())
			if traceID == "" {
				traceID = r.Header.Get("X-Request-ID")
			}
			traceID = strings.TrimSpace(traceID)
			if traceID == "" {
				traceID = "trace-missing"
			}
			w.Header().Set("X-Trace-ID", traceID)

			start := time.Now()
			next.ServeHTTP(w, r)
			logger.Info("http request completed",
				"trace_id", traceID,
				"method", r.Method,
				"path", r.URL.Path,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

func routeLabel(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pat := rc.RoutePattern(); pat != "" {
			return pat
		}
	}
	return "unmatched"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func logLevel() slog.Level {
	switch os.Getenv("OPS_LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
