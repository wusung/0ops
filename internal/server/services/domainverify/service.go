package domainverify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Binding mirrors the spec § 4.1 domain_binding fields used by this package.
// The DB schema migration that materializes is_apex / extends_used /
// health_check_failed_at / status is intentionally deferred (see doc.go).
type Binding struct {
	ID                  string
	TeamID              string
	AppID               string
	Hostname            string
	Kind                string
	Status              Status
	Verified            bool
	VerificationToken   string
	IsApex              bool
	ExtendsUsed         int
	ExpiresAt           time.Time
	HealthCheckFailedAt *time.Time
	CFHostnameID        string
	CreatedAt           time.Time
	VerifiedAt          *time.Time
}

// Store abstracts the persistence layer for `domain_binding` rows.
// Implementations must enforce hostname uniqueness on Insert.
type Store interface {
	GetByHostname(ctx context.Context, hostname string) (Binding, error)
	Insert(ctx context.Context, b Binding) error
	SetCloudflareHostnameID(ctx context.Context, id, cfHostnameID string) error
	UpdateVerified(ctx context.Context, id string, when time.Time) error
	UpdateExpired(ctx context.Context, id string) error
	UpdateExtendsUsed(ctx context.Context, id string, extendsUsed int, expiresAt time.Time) error
	UpdateUnhealthyMark(ctx context.Context, id string, failedAt *time.Time) error
	UpdateReleased(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	ListPending(ctx context.Context) ([]Binding, error)
	ListVerified(ctx context.Context) ([]Binding, error)
	ListExpiredCandidates(ctx context.Context) ([]Binding, error)
}

// CloudflareHostnameAPI abstracts the Custom Hostname endpoints. The concrete
// implementation lives outside this package (see doc.go).
type CloudflareHostnameAPI interface {
	RegisterCustomHostname(ctx context.Context, hostname string) (cfHostnameID string, err error)
	ActivateCustomHostname(ctx context.Context, cfHostnameID string) error
	DeleteCustomHostname(ctx context.Context, cfHostnameID string) error
}

// AuditEvent is the durable record emitted on state-changing actions.
type AuditEvent struct {
	Action     string
	Hostname   string
	TeamID     string
	BindingID  string
	PreviewID  string
	Detail     map[string]any
	OccurredAt time.Time
}

// Auditor abstracts the audit-log writer.
type Auditor interface {
	Record(ctx context.Context, event AuditEvent) error
}

// PlanGate decides whether a plan tier may add an `extra` hostname (spec § 9).
type PlanGate interface {
	AllowExtra(planTier string) bool
}

// Standard sentinel errors.
var (
	// ErrBindingNotFound is returned when no domain_binding row matches the
	// requested hostname or id.
	ErrBindingNotFound = errors.New("binding not found")
	// ErrHostnameTaken is returned when the hostname is already bound
	// (cross-team uniqueness, spec § 5.2 step 4).
	ErrHostnameTaken = errors.New("hostname taken")
	// ErrPlanRequired is returned when the team plan tier cannot add extras.
	ErrPlanRequired = errors.New("plan tier required")
)

// AddArgs are the inputs for both PlanAdd (preview) and ConfirmAdd.
type AddArgs struct {
	TeamID      string
	ActorUserID string
	AppID       string
	Hostname    string
	PlanTier    string
}

// AddPlan is the preview output containing user-facing DNS instructions and
// the values that will be persisted on confirm.
type AddPlan struct {
	Args              AddArgs
	IsApex            bool
	VerificationToken string
	CNAMETarget       string
	TXTName           string
	TXTValue          string
	ExpiresAt         time.Time
	ApexCompatibility []ApexProvider
}

// ConfirmAddInput carries the plan + preview id (idempotency key).
type ConfirmAddInput struct {
	Plan      AddPlan
	PreviewID string
}

// VerifyArgs targets a single binding for a one-shot verify attempt.
type VerifyArgs struct {
	Hostname string
}

// VerifyOutcome reports the result of a verify attempt.
type VerifyOutcome struct {
	Hostname  string
	Verified  bool
	LastError string
}

// ServiceConfig wires service dependencies.
type ServiceConfig struct {
	Store        Store
	Cloudflare   CloudflareHostnameAPI
	Resolver     Resolver
	Auditor      Auditor
	PlanGate     PlanGate
	TunnelTarget string
	Now          func() time.Time
	NewID        func() string
}

// Service orchestrates domain add / verify / extend over the spec state machine.
type Service struct {
	store        Store
	cf           CloudflareHostnameAPI
	resolver     Resolver
	auditor      Auditor
	planGate     PlanGate
	tunnelTarget string
	now          func() time.Time
	newID        func() string
}

// NewService constructs a Service with defaults for Now/NewID.
func NewService(cfg ServiceConfig) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	return &Service{
		store:        cfg.Store,
		cf:           cfg.Cloudflare,
		resolver:     cfg.Resolver,
		auditor:      cfg.Auditor,
		planGate:     cfg.PlanGate,
		tunnelTarget: strings.TrimSuffix(cfg.TunnelTarget, "."),
		now:          now,
		newID:        newID,
	}
}

// PlanAdd validates inputs and returns the side_effects payload (spec § 5.2).
func (s *Service) PlanAdd(ctx context.Context, args AddArgs) (AddPlan, error) {
	if s.planGate == nil || !s.planGate.AllowExtra(args.PlanTier) {
		return AddPlan{}, fmt.Errorf("%w: plan tier %q cannot add extra hostnames", ErrPlanRequired, args.PlanTier)
	}
	if err := ValidateHostname(args.Hostname); err != nil {
		return AddPlan{}, err
	}
	isApex, err := DetectApex(args.Hostname)
	if err != nil {
		return AddPlan{}, err
	}
	if _, err := s.store.GetByHostname(ctx, args.Hostname); err == nil {
		return AddPlan{}, ErrHostnameTaken
	} else if !errors.Is(err, ErrBindingNotFound) {
		return AddPlan{}, err
	}
	token, err := GenerateVerificationToken()
	if err != nil {
		return AddPlan{}, err
	}
	plan := AddPlan{
		Args:              args,
		IsApex:            isApex,
		VerificationToken: token,
		CNAMETarget:       s.tunnelTarget,
		TXTName:           "_0ops-verify." + args.Hostname,
		TXTValue:          token,
		ExpiresAt:         s.now().UTC().Add(24 * time.Hour),
	}
	if isApex {
		plan.ApexCompatibility = IncompatibleApexProviders()
	}
	return plan, nil
}

// ConfirmAdd performs the spec § 5.4 reversible side-effects:
//  1. INSERT domain_binding row (reversible via Delete).
//  2. Register Cloudflare Custom Hostname (reversible via DELETE custom hostname).
//
// On Cloudflare failure the inserted row is removed so the preview can be
// retried; this matches ADR-0002 reversible compensation ordering.
func (s *Service) ConfirmAdd(ctx context.Context, in ConfirmAddInput) (Binding, error) {
	plan := in.Plan
	id := s.newID()
	now := s.now().UTC()
	binding := Binding{
		ID:                id,
		TeamID:            plan.Args.TeamID,
		AppID:             plan.Args.AppID,
		Hostname:          plan.Args.Hostname,
		Kind:              "extra",
		Status:            StatusPending,
		Verified:          false,
		VerificationToken: plan.VerificationToken,
		IsApex:            plan.IsApex,
		ExtendsUsed:       0,
		ExpiresAt:         plan.ExpiresAt,
		CreatedAt:         now,
	}
	if err := s.store.Insert(ctx, binding); err != nil {
		return Binding{}, err
	}
	cfID, err := s.cf.RegisterCustomHostname(ctx, plan.Args.Hostname)
	if err != nil {
		if delErr := s.store.Delete(ctx, id); delErr != nil {
			return Binding{}, fmt.Errorf("register custom hostname: %w (rollback failed: %v)", err, delErr)
		}
		return Binding{}, fmt.Errorf("register custom hostname: %w", err)
	}
	if err := s.store.SetCloudflareHostnameID(ctx, id, cfID); err != nil {
		if delErr := s.cf.DeleteCustomHostname(ctx, cfID); delErr != nil {
			return Binding{}, fmt.Errorf("persist cf hostname id: %w (cf rollback failed: %v)", err, delErr)
		}
		if delErr := s.store.Delete(ctx, id); delErr != nil {
			return Binding{}, fmt.Errorf("persist cf hostname id: %w (rollback failed: %v)", err, delErr)
		}
		return Binding{}, fmt.Errorf("persist cf hostname id: %w", err)
	}
	binding.CFHostnameID = cfID
	_ = s.auditor.Record(ctx, AuditEvent{
		Action: "domain_add", Hostname: plan.Args.Hostname,
		TeamID: plan.Args.TeamID, BindingID: id, PreviewID: in.PreviewID,
		OccurredAt: now,
		Detail: map[string]any{
			"is_apex":        plan.IsApex,
			"expires_at":     plan.ExpiresAt,
			"cf_hostname_id": cfID,
		},
	})
	return binding, nil
}

// Verify runs the dual-condition DNS check for a single binding. On pass it
// flips status to verified and activates the Cloudflare hostname.
func (s *Service) Verify(ctx context.Context, args VerifyArgs) (VerifyOutcome, error) {
	binding, err := s.store.GetByHostname(ctx, args.Hostname)
	if err != nil {
		return VerifyOutcome{}, err
	}
	verifyErr := DualCondition(ctx, s.resolver, VerifyInput{
		Hostname:     binding.Hostname,
		IsApex:       binding.IsApex,
		Token:        binding.VerificationToken,
		TunnelTarget: s.tunnelTarget,
	})
	stage := "pending"
	if binding.Status == StatusVerified || binding.Status == StatusUnhealthy {
		stage = "active"
	}
	if verifyErr != nil {
		recordVerifyAttempt(stage, classifyVerifyOutcome(verifyErr))
		return VerifyOutcome{Hostname: binding.Hostname, Verified: false, LastError: verifyErr.Error()}, nil
	}
	if binding.Status == StatusPending {
		now := s.now().UTC()
		if err := s.store.UpdateVerified(ctx, binding.ID, now); err != nil {
			return VerifyOutcome{}, err
		}
		if binding.CFHostnameID != "" {
			if err := s.cf.ActivateCustomHostname(ctx, binding.CFHostnameID); err != nil {
				return VerifyOutcome{}, fmt.Errorf("activate hostname: %w", err)
			}
		}
		_ = s.auditor.Record(ctx, AuditEvent{
			Action: "domain_verified", Hostname: binding.Hostname,
			TeamID: binding.TeamID, BindingID: binding.ID,
			OccurredAt: now,
		})
	}
	recordVerifyAttempt(stage, "success")
	return VerifyOutcome{Hostname: binding.Hostname, Verified: true}, nil
}

// Extend bumps expires_at + 24h, capped at 2 extensions.
func (s *Service) Extend(ctx context.Context, hostname string) (ExtendResult, error) {
	binding, err := s.store.GetByHostname(ctx, hostname)
	if err != nil {
		return ExtendResult{}, err
	}
	out, err := ApplyExtend(ExtendInput{
		Now:         s.now().UTC(),
		Verified:    binding.Verified,
		ExtendsUsed: binding.ExtendsUsed,
		ExpiresAt:   binding.ExpiresAt,
	})
	if err != nil {
		return ExtendResult{}, err
	}
	if err := s.store.UpdateExtendsUsed(ctx, binding.ID, out.NewExtendsUsed, out.NewExpiresAt); err != nil {
		return ExtendResult{}, err
	}
	_ = s.auditor.Record(ctx, AuditEvent{
		Action: "domain_extend", Hostname: hostname,
		TeamID: binding.TeamID, BindingID: binding.ID,
		OccurredAt: s.now().UTC(),
		Detail: map[string]any{
			"extends_used": out.NewExtendsUsed,
			"expires_at":   out.NewExpiresAt,
		},
	})
	return out, nil
}

func classifyVerifyOutcome(err error) string {
	switch {
	case errors.Is(err, ErrCNAMENotMatched):
		return "cname_missing"
	case errors.Is(err, ErrTXTNotMatched):
		return "txt_missing"
	default:
		return "error"
	}
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("randomID: %w", err))
	}
	return hex.EncodeToString(buf)
}
