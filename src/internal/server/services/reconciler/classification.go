package reconciler

import "strings"

// Classification is the canonical enum used by deploy_run.failure_classification
// (spec § 7.1). The reconciler must classify every failed / rolled_back /
// failed_permanently transition; unknown is the explicit "needs engineer
// review" bucket, and its share is tracked by a dashboard panel
// (§ 7.3 + § 16 #6).
type Classification string

const (
	ClassBuildpackDetectFailed Classification = "buildpack_detect_failed"
	ClassBuildCompileError     Classification = "build_compile_error"
	ClassBuildTimeout          Classification = "build_timeout"
	ClassRegistryAuthFailed    Classification = "registry_auth_failed"
	ClassRegistryPushFailed    Classification = "registry_push_failed"
	ClassImageScanBlocked      Classification = "image_scan_blocked"
	ClassGitOpsPushConflict    Classification = "gitops_push_conflict"
	ClassArgoSyncTimeout       Classification = "argo_sync_timeout"
	ClassHealthCheckFailed     Classification = "health_check_failed"
	ClassCloudflareAPIFailed   Classification = "cloudflare_api_failed"
	ClassBuildSecretExpired    Classification = "build_secret_expired"
	ClassRepoCheckoutFailed    Classification = "repo_checkout_failed"
	ClassUnknown               Classification = "unknown"
)

// AllClassifications is the full enumeration in the order the
// dashboard / docs use. Useful for self-checks and metric pre-warming.
var AllClassifications = []Classification{
	ClassBuildpackDetectFailed,
	ClassBuildCompileError,
	ClassBuildTimeout,
	ClassRegistryAuthFailed,
	ClassRegistryPushFailed,
	ClassImageScanBlocked,
	ClassGitOpsPushConflict,
	ClassArgoSyncTimeout,
	ClassHealthCheckFailed,
	ClassCloudflareAPIFailed,
	ClassBuildSecretExpired,
	ClassRepoCheckoutFailed,
	ClassUnknown,
}

// IsValid reports whether the given string matches a known
// classification.  Used by the DB-bound lint helpers.
func IsValid(c Classification) bool {
	switch c {
	case ClassBuildpackDetectFailed, ClassBuildCompileError, ClassBuildTimeout,
		ClassRegistryAuthFailed, ClassRegistryPushFailed,
		ClassImageScanBlocked, ClassGitOpsPushConflict,
		ClassArgoSyncTimeout, ClassHealthCheckFailed,
		ClassCloudflareAPIFailed, ClassBuildSecretExpired,
		ClassRepoCheckoutFailed, ClassUnknown:
		return true
	default:
		return false
	}
}

// WorkflowRunOutcome enumerates the GitHub Actions completion shapes
// the reconciler needs to map onto a classification. ConclusionTimedOut
// is split out of "failure" so the build-timeout class lands cleanly.
type WorkflowRunOutcome struct {
	Status     string // "completed" | "in_progress" | "queued"
	Conclusion string // "success" | "failure" | "timed_out" | "cancelled" | ...
	StepName   string // the failing step name, when known
	StepLog    string // a free-form log excerpt; substring match only
}

// ClassifyWorkflowRun maps the GHA outcome onto a Classification. The
// mapping mirrors the spec § 7.2 table; any signal that does not match
// falls back to ClassUnknown so the engineer-review panel surfaces it.
// The match favours specific step-name signals over generic logs.
func ClassifyWorkflowRun(out WorkflowRunOutcome) Classification {
	switch strings.ToLower(out.Conclusion) {
	case "timed_out":
		return ClassBuildTimeout
	case "cancelled":
		return ClassUnknown
	}
	stepLog := strings.ToLower(out.StepLog)
	step := strings.ToLower(out.StepName)

	switch {
	case strings.Contains(step, "checkout") || strings.Contains(stepLog, "git checkout"):
		return ClassRepoCheckoutFailed
	case strings.Contains(step, "docker login") || strings.Contains(stepLog, "docker login") ||
		strings.Contains(stepLog, "unauthorized") && strings.Contains(stepLog, "registry"):
		return ClassRegistryAuthFailed
	case strings.Contains(step, "trivy") || strings.Contains(stepLog, "trivy"):
		return ClassImageScanBlocked
	case strings.Contains(step, "pack build") || strings.Contains(stepLog, "pack build"):
		if strings.Contains(stepLog, "no buildpack groups passed") {
			return ClassBuildpackDetectFailed
		}
		if strings.Contains(stepLog, "publish") || strings.Contains(stepLog, "push") {
			return ClassRegistryPushFailed
		}
		return ClassBuildCompileError
	case strings.Contains(step, "git push") || strings.Contains(stepLog, "fast-forward"):
		return ClassGitOpsPushConflict
	case strings.Contains(stepLog, "ops_token") || strings.Contains(stepLog, "ops token expired"):
		return ClassBuildSecretExpired
	}
	return ClassUnknown
}

// ArgoCDHealth enumerates the ArgoCD Application health values the
// reconciler maps to a Classification when a sync stalls past the
// spec § 8.2 threshold.
type ArgoCDHealth struct {
	Health  string // "Healthy" | "Degraded" | "Progressing" | "Suspended" | "Missing" | ...
	Sync    string // "Synced" | "OutOfSync"
}

// ClassifyArgoCD maps the ArgoCD Application status onto a
// Classification. "Healthy" should never reach this function — the
// caller transitions to live instead.
func ClassifyArgoCD(h ArgoCDHealth) Classification {
	switch strings.ToLower(h.Health) {
	case "degraded":
		return ClassHealthCheckFailed
	case "missing", "outofsync":
		return ClassArgoSyncTimeout
	}
	if strings.EqualFold(h.Sync, "OutOfSync") {
		return ClassArgoSyncTimeout
	}
	return ClassArgoSyncTimeout
}

// ClassifyCloudflareFailure tags Cloudflare retry-failures from the
// rate-limit / tunnel control plane (spec § 7.2). The function exists
// so callers do not embed string literals far from the spec table.
func ClassifyCloudflareFailure() Classification { return ClassCloudflareAPIFailed }

// String exposes the underlying enum value for ergonomic logging.
func (c Classification) String() string { return string(c) }

// Ptr returns a pointer to the receiver, useful when wiring optional
// pointer fields without a temporary variable.
func (c Classification) Ptr() *string {
	s := string(c)
	return &s
}
