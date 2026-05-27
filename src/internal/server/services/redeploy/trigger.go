package redeploy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/winshare/zeroops/internal/server/db"
	workflowdispatch "github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// Source identifies who caused the deploy_run row to exist. Mirrors the
// `deploy_run.source` enum constrained in migration 00005 (spec § 12).
type Source string

const (
	// SourceUser indicates a CLI/MCP-initiated redeploy that traversed the
	// preview-confirm gate.
	SourceUser Source = "user"
	// SourceWebhook indicates a `push` webhook delivered by GitHub.
	SourceWebhook Source = "webhook"
	// SourceReconciler indicates a reconciler-tick replay (M5 onwards).
	SourceReconciler Source = "reconciler"
)

// TriggerArgs is the contract handed to Trigger by both the push handler and
// the Confirm path. The two callers pre-resolve commit_sha and trace_id so
// Trigger itself stays free of GitHub API dependencies.
type TriggerArgs struct {
	TeamID            string
	TeamSlug          string
	AppID             string
	AppSlug           string
	RepoURL           string
	Ref               string
	CommitSHA         string
	Source            Source
	ActorUserID       string // empty when Source == SourceWebhook
	WebhookDeliveryID string // populated only when Source == SourceWebhook
	TraceID           string
}

// TriggerResult describes the artefacts produced by a single Trigger
// invocation. ImageRef is the predicted GHCR tag the GHA workflow will push
// to; durable image_ref reconciliation lands in M2.x callback path.
type TriggerResult struct {
	DeployRunID string
	CommitSHA   string
	TraceID     string
	ImageRef    string
	SubdomainURL string
}

// TriggerStore captures the DB operations required to spawn a deploy_run.
// A subset of *db.Repository; tests use a fake.
type TriggerStore interface {
	InsertRedeployRun(ctx context.Context, params db.InsertRedeployRunParams) (db.InsertRedeployRunResult, error)
}

// Dispatcher triggers the GHA workflow. Same contract as createapp.Dispatcher
// so we can pass the same *workflowdispatch.Client; redeclared here to keep
// the service self-contained.
type Dispatcher interface {
	Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error
}

// OpsTokenSigner issues the short-lived JWT-shaped token the GHA workflow
// presents back on callback (ADR-0005 § 4 point 6).
type OpsTokenSigner interface {
	Issue(runID, traceID string, scopes []string) (string, error)
}

// Trigger captures the shared deploy_run insert + workflow dispatch path
// invoked by both the webhook push handler and the user redeploy confirm.
// Callers MUST have already enforced their policy (paused / in-flight /
// rate-limit) before calling here.
type Trigger struct {
	store           TriggerStore
	dispatcher      Dispatcher
	tokenSigner     OpsTokenSigner
	callbackBaseURL string
}

// NewTrigger wires the dependencies. dispatcher/tokenSigner may be nil in
// dev environments without GitHub App credentials; in that case Trigger
// inserts the deploy_run row but skips the dispatch step. This mirrors the
// degraded fallback used in createapp.Service.
func NewTrigger(store TriggerStore, dispatcher Dispatcher, tokenSigner OpsTokenSigner, callbackBaseURL string) *Trigger {
	return &Trigger{
		store:           store,
		dispatcher:      dispatcher,
		tokenSigner:     tokenSigner,
		callbackBaseURL: strings.TrimRight(callbackBaseURL, "/"),
	}
}

// Trigger inserts a deploy_run row attributing the source/actor/delivery_id
// and (if configured) dispatches the GHA workflow. Caller-supplied
// args.TraceID is preserved verbatim so trace_id propagates across the
// webhook → backend → GHA → callback boundary (ADR-0005 § 4 point 6;
// ADR-0006 § 4.4).
func (t *Trigger) Trigger(ctx context.Context, args TriggerArgs) (TriggerResult, error) {
	if t.store == nil {
		return TriggerResult{}, errors.New("redeploy: nil store")
	}
	if args.TeamID == "" || args.AppID == "" {
		return TriggerResult{}, errors.New("redeploy: team_id and app_id required")
	}
	if args.Source == "" {
		args.Source = SourceUser
	}
	if args.Source == SourceWebhook && args.WebhookDeliveryID == "" {
		return TriggerResult{}, errors.New("redeploy: webhook source missing delivery_id")
	}

	params := db.InsertRedeployRunParams{
		TeamID:            args.TeamID,
		AppID:             args.AppID,
		Ref:               args.Ref,
		CommitSHA:         args.CommitSHA,
		TraceID:           args.TraceID,
		Source:            string(args.Source),
		ActorUserID:       args.ActorUserID,
		WebhookDeliveryID: args.WebhookDeliveryID,
	}
	row, err := t.store.InsertRedeployRun(ctx, params)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("insert deploy_run: %w", err)
	}

	imageRef := fmt.Sprintf("ghcr.io/winshare/0ops-apps/%s/%s:%s", args.TeamSlug, args.AppSlug, row.DeployRunID)
	subdomain := fmt.Sprintf("https://%s.winshare.tw", args.AppSlug)

	if t.dispatcher != nil {
		if t.tokenSigner == nil {
			return TriggerResult{}, errors.New("redeploy: missing token signer")
		}
		token, err := t.tokenSigner.Issue(row.DeployRunID, args.TraceID, []string{"ghcr:push", "callback:write"})
		if err != nil {
			return TriggerResult{}, fmt.Errorf("sign ops_token: %w", err)
		}
		if err := t.dispatcher.Dispatch(ctx, workflowdispatch.ClientPayload{
			RunID:       row.DeployRunID,
			AppSlug:     args.AppSlug,
			TeamSlug:    args.TeamSlug,
			CommitSHA:   args.CommitSHA,
			Ref:         args.Ref,
			ImageRef:    imageRef,
			OpsToken:    token,
			CallbackURL: fmt.Sprintf("%s/internal/deploy-runs/%s/callback", t.callbackBaseURL, row.DeployRunID),
			TraceID:     args.TraceID,
		}); err != nil {
			return TriggerResult{}, fmt.Errorf("workflow dispatch: %w", err)
		}
	}

	return TriggerResult{
		DeployRunID:  row.DeployRunID,
		CommitSHA:    args.CommitSHA,
		TraceID:      args.TraceID,
		ImageRef:     imageRef,
		SubdomainURL: subdomain,
	}, nil
}
