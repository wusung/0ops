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
	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
	"github.com/winshare/zeroops/internal/server/leader"
	ratelimit "github.com/winshare/zeroops/internal/server/middleware/ratelimit"
	"github.com/winshare/zeroops/internal/server/observability"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/server/services/cloudflare"
	"github.com/winshare/zeroops/internal/server/services/k3s"
	"github.com/winshare/zeroops/internal/server/services/reconciler"
	"github.com/winshare/zeroops/internal/shared"
	"github.com/winshare/zeroops/internal/shared/runtime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	slog.SetDefault(logger)

	// Fail fast if any dev-only knob (file:// repo, local build pipeline)
	// is enabled while OPS_ENV=production. Per ADR-0012 § 3.1.
	runtime.AssertProductionSafe()

	addr := envOr("OPS_LISTEN_ADDR", ":8080")
	metrics := observability.NewMetrics()
	pool, err := db.NewPool(context.Background(), db.ConfigFromEnv())
	if err != nil {
		logger.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Spec § 16 hard rule #10 — refuse to serve traffic if DATABASE_URL
	// resolved to the read-only replica instead of postgres-main.
	if err := db.EnsurePrimary(context.Background(), db.PoolProbe{Pool: pool}); err != nil {
		logger.Error("postgres primary check failed", "err", err)
		os.Exit(1)
	}

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

	reconObserver := newReconcilerObserver(metrics)
	incidentSvc := reconciler.NewIncidentService(repo, auditAdapter{svc: auditSvc}, reconObserver)

	// Construct the on-disk ingestion store (M6.8 / ADR-0013 §11).
	// Limits are intentionally fixed at spec §11 values; T20 will make them
	// configurable per plan tier.
	ingestRoot := envOr("APP_SOURCE_INGEST_ROOT", "/var/lib/0ops/uploads")
	ingestStore := &ingestion.Store{
		Root:            ingestRoot,
		MaxArchiveBytes: 100 << 20, // 100 MB
		MaxEntryBytes:   50 << 20,  // 50 MB
		MaxEntries:      10000,
	}

	// Construct the build token signer for the archive download path (M6.9).
	// runtime.AssertProductionSafe (T4) already ensures buildSecret is non-empty
	// in production. In dev without OPS_BUILD_TOKEN_SECRET, archiveSigner is nil
	// and the GET /v1/uploads/{id}/archive route is not registered.
	var archiveSigner *ingestion.TokenSigner
	if buildSecret := os.Getenv("OPS_BUILD_TOKEN_SECRET"); buildSecret != "" {
		archiveSigner = &ingestion.TokenSigner{
			Secret: []byte(buildSecret),
			TTL:    15 * time.Minute, // spec §8.2
		}
	}

	r.Mount("/", appserver.NewRouterWithIngestion(repo, k3sClient, cfClient, limiter, metrics.RateLimitObserver(), auditSvc, incidentSvc, ingestStore, auditSvc, archiveSigner))

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go limiter.RunCleanup(ctx, time.Hour)

	leaderMode := envOr("OPS_LEADER_MODE", "always")
	identity := leader.PodIdentity()
	ldr, runLeader, err := buildLeader(leaderMode, identity, metrics, os.Getenv("OPS_LEADER_KUBECONFIG"))
	if err != nil {
		logger.Error("leader bootstrap failed", "err", err, "mode", leaderMode)
		os.Exit(1)
	}
	if runLeader != nil {
		go runLeader(ctx)
		logger.Info("leader election started", "mode", leaderMode, "identity", identity)
	} else {
		// AlwaysLeader path: seed leader_status=1 so /metrics reflects
		// the v1 single-replica reality even before any reconciler tick.
		metrics.SetLeaderStatus(identity, true)
		logger.Info("leader election bypassed", "mode", leaderMode, "identity", identity)
	}

	startReconciler(ctx, logger, repo, incidentSvc, reconObserver, k3sClient, ldr)

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

// auditAdapter bridges audit.Service.Log onto reconciler.AuditWriter so
// the incident close path can write an audit_log entry without creating
// a circular dependency between the two services.
type auditAdapter struct {
	svc *audit.Service
}

func (a auditAdapter) Log(ctx context.Context, entry reconciler.AuditEntry) error {
	if a.svc == nil {
		return nil
	}
	source := audit.Source(entry.Source)
	if source == "" {
		source = audit.SourceUser
	}
	outcome := audit.Outcome(entry.Outcome)
	if outcome == "" {
		outcome = audit.OutcomeSuccess
	}
	return a.svc.Log(ctx, audit.Entry{
		TeamID:      entry.TeamID,
		ActorUserID: entry.ActorUserID,
		Source:      source,
		SubjectType: entry.SubjectType,
		SubjectID:   entry.SubjectID,
		Action:      entry.Action,
		Args:        entry.Args,
		Result:      entry.Result,
		TraceID:     entry.TraceID,
		Outcome:     outcome,
	})
}

// reconcilerObserver wires reconciler.Observer onto the prometheus
// registry. Every metric here is registered up-front in
// observability.NewMetrics so the dashboard panels have stable label
// sets even before the first reconciler tick fires.
type reconcilerObserver struct {
	m *observability.Metrics
}

func newReconcilerObserver(m *observability.Metrics) reconciler.Observer {
	return &reconcilerObserver{m: m}
}

func (o *reconcilerObserver) ObserveTick(kind, outcome string)      { o.m.ObserveReconcilerTick(kind, outcome) }
func (o *reconcilerObserver) ObserveJobTerminal(kind, outcome string) {
	o.m.ObserveReconcilerJobTerminal(kind, outcome)
}
func (o *reconcilerObserver) ObserveDeployTransition(_, _ string) {}
func (o *reconcilerObserver) ObserveFailureClassification(classification string) {
	o.m.ObserveFailureClassification(classification)
}
func (o *reconcilerObserver) ObserveIncidentOpened(kind, severity string) {
	o.m.ObserveIncidentOpened(kind, severity)
}
func (o *reconcilerObserver) ObserveIncidentClosed(kind, severity string) {
	o.m.ObserveIncidentClosed(kind, severity)
}
func (o *reconcilerObserver) SetPendingJobs(kind string, count int) {
	o.m.SetReconciliationJobsPending(kind, float64(count))
}
func (o *reconcilerObserver) SetOpenIncidents(severity string, count int) {
	o.m.SetOpenIncidents(severity, float64(count))
}

// startReconciler builds the M5.3 reconciler.Runner from cmd/server
// state. Scanners are wired with whatever credentials are available —
// without a GitHub or ArgoCD client they degrade to a no-op tick. The
// job processor + leader gate always start; the gate is driven by the
// supplied leader.Leader (AlwaysLeader in dev, LeaseLeader in M5+ prod).
func startReconciler(ctx context.Context, logger *slog.Logger, repo *db.Repository, incidentSvc *reconciler.IncidentService, observer reconciler.Observer, k3sClient *k3s.Client, ldr leader.Leader) {
	scanners := buildScanners(repo, incidentSvc, observer, k3sClient)
	cfg := reconciler.Config{
		Leader:              reconcilerLeaderGate{l: ldr},
		Store:               repo,
		Logger:              logger,
		Observer:            observer,
		Incidents:           incidentSvc,
		Handlers:            reconciler.NewHandlerRegistry(),
		DeployStatusScanner: scanners.deploy,
		ArgoSyncScanner:     scanners.argo,
	}
	runner := reconciler.New(cfg)
	runner.Start(ctx)
	logger.Info("reconciler started", "identity", ldr.Identity(), "loops", scanners.summary())
}

type scannerSet struct {
	deploy *reconciler.DeployStatusScanner
	argo   *reconciler.ArgoSyncScanner
}

func (s scannerSet) summary() string {
	switch {
	case s.deploy != nil && s.argo != nil:
		return "deploy_status,argo_sync,job_queue,metrics"
	case s.deploy != nil:
		return "deploy_status,job_queue,metrics"
	case s.argo != nil:
		return "argo_sync,job_queue,metrics"
	default:
		return "job_queue,metrics"
	}
}

func buildScanners(repo *db.Repository, incidents *reconciler.IncidentService, observer reconciler.Observer, k3sClient *k3s.Client) scannerSet {
	var set scannerSet
	if k3sClient != nil {
		set.argo = reconciler.NewArgoSyncScanner(repo, argoCDAdapter{client: k3sClient}, observer)
	}
	// deploy_status scanner depends on GHA credentials; wire only when
	// env vars are configured. Absence is fine in dev — the runner will
	// emit a tick-skipped metric and move on.
	set.deploy = reconciler.NewDeployStatusScanner(repo, nil, incidents, observer)
	return set
}

// argoCDAdapter bridges k3s.Client.GetApplicationStatus onto the
// reconciler.ApplicationStatusFetcher interface so the scanner does not
// import k3s directly.
type argoCDAdapter struct {
	client *k3s.Client
}

func (a argoCDAdapter) GetApplicationStatus(ctx context.Context, teamSlug, appSlug string) (reconciler.ApplicationStatus, error) {
	if a.client == nil {
		return reconciler.ApplicationStatus{}, nil
	}
	status, err := a.client.GetApplicationStatus(ctx, teamSlug, appSlug)
	if err != nil {
		return reconciler.ApplicationStatus{}, err
	}
	return reconciler.ApplicationStatus{
		SyncStatus:   status.SyncStatus,
		HealthStatus: status.HealthStatus,
	}, nil
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
