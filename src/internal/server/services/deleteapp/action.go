package deleteapp

import (
	"errors"
	"fmt"

	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// PreviewAction is the action string persisted in the preview row.
const PreviewAction = "delete_app"

// SubjectType is the audit_log subject_type for delete_app rows.
const SubjectType = "app"

// Standard error values surfaced to handlers; map them to apperror codes
// upstream. Keeping them typed lets callers compose error checks via
// errors.Is in tests.
var (
	// ErrValidationFailed signals a malformed args payload.
	ErrValidationFailed = errors.New("validation failed")
	// ErrConfirmMismatch signals args.Confirm != app_slug (spec § 4.1).
	ErrConfirmMismatch = errors.New("confirm mismatch")
	// ErrAppNotFound signals the app row was not located within the team scope.
	ErrAppNotFound = errors.New("app not found")
	// ErrAlreadyDeleting signals an existing delete is already in progress
	// (spec § 4.2 already_deleting).
	ErrAlreadyDeleting = errors.New("app already deleting")
	// ErrPreviewNotFound signals the preview row was not found / wrong team.
	ErrPreviewNotFound = errors.New("preview not found")
	// ErrPreviewConsumed signals a consumed preview without replay data.
	ErrPreviewConsumed = errors.New("preview consumed")
	// ErrPreviewExpired signals an expired preview (10 min TTL).
	ErrPreviewExpired = errors.New("preview expired")
	// ErrCompensateMarked signals reversible execution failed and the app
	// was marked delete_compensated; cleanup_residue is enqueued.
	ErrCompensateMarked = errors.New("delete compensated")
)

// SideEffectsForApp builds the deterministic 5-effect list used in the
// preview response (spec § 4.3). Counts are derived from the bindings list
// so the user sees the actual scope of the deletion.
//
// Effect 5 (ArgoCD prune) is the only irreversible step.
func SideEffectsForApp(bindings []db.AppDomainBinding) []dto.SideEffect {
	extras := 0
	for _, b := range bindings {
		if b.Kind == "extra" {
			extras++
		}
	}
	return []dto.SideEffect{
		{
			Effect:      "cancel_inflight_deploys",
			Reversible:  true,
			Description: "Cancel any in-flight builds for this app",
		},
		{
			Effect:      "unbind_custom_hostnames",
			Reversible:  true,
			Description: fmt.Sprintf("Unbind %d custom hostnames from Cloudflare", extras),
		},
		{
			Effect:      "delete_domain_bindings",
			Reversible:  true,
			Description: "Remove domain bindings from DB",
		},
		{
			Effect:      "remove_gitops_manifest",
			Reversible:  true,
			Description: "Remove app manifest from 0ops-gitops",
		},
		{
			Effect:      "argocd_prune_resources",
			Reversible:  false,
			Description: "ArgoCD will prune Kubernetes resources (irreversible)",
		},
	}
}

// SummaryForApp is the action_summary stored on the preview row.
func SummaryForApp(teamSlug, appSlug string) string {
	return fmt.Sprintf("Delete app %q (@ team %q) — this operation cannot be undone", appSlug, teamSlug)
}

// ValidateArgs checks the args payload against spec § 4.1 — appSlug is
// already encoded in the URL path; Confirm is supplied in the body and
// must match it character-for-character.
func ValidateArgs(appSlug, confirm string) error {
	if appSlug == "" {
		return fmt.Errorf("%w: app_slug is required", ErrValidationFailed)
	}
	if confirm == "" {
		return fmt.Errorf("%w: confirm is required", ErrValidationFailed)
	}
	if confirm != appSlug {
		return ErrConfirmMismatch
	}
	return nil
}
