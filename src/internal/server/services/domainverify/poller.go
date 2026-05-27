package domainverify

import (
	"context"
	"errors"
	"strings"
	"time"
)

// DefaultPollerTick is the spec § 6.1 30-second tick.
const DefaultPollerTick = 30 * time.Second

// LeaderProbe gates polling to a single backend pod in M5+ HA setups.
type LeaderProbe interface {
	IsLeader(ctx context.Context) bool
}

// PollerConfig wires the poller dependencies.
type PollerConfig struct {
	Service      *Service
	Store        Store
	Resolver     Resolver
	Cloudflare   CloudflareHostnameAPI
	Auditor      Auditor
	Leader       LeaderProbe
	Now          func() time.Time
	TunnelTarget string
	Tick         time.Duration
}

// Poller runs verifyPending / checkUnhealthy / cleanupExpired on a fixed tick.
type Poller struct {
	cfg PollerConfig
}

// NewPoller constructs a Poller; defaults Tick to DefaultPollerTick and Now to time.Now.
func NewPoller(cfg PollerConfig) *Poller {
	if cfg.Tick == 0 {
		cfg.Tick = DefaultPollerTick
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.TunnelTarget = strings.TrimSuffix(cfg.TunnelTarget, ".")
	return &Poller{cfg: cfg}
}

// RunLoop blocks until ctx is done, ticking once per Tick interval.
func (p *Poller) RunLoop(ctx context.Context) {
	t := time.NewTicker(p.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = p.RunOnce(ctx)
		}
	}
}

// RunOnce runs the three sweeps in order; skips entirely when not leader.
func (p *Poller) RunOnce(ctx context.Context) error {
	if p.cfg.Leader != nil && !p.cfg.Leader.IsLeader(ctx) {
		return nil
	}
	if err := p.verifyPending(ctx); err != nil {
		return err
	}
	if err := p.checkUnhealthy(ctx); err != nil {
		return err
	}
	return p.cleanupExpired(ctx)
}

func (p *Poller) verifyPending(ctx context.Context) error {
	started := p.cfg.Now()
	defer func() { recordPollerTick("verifyPending", time.Since(started)) }()
	rows, err := p.cfg.Store.ListPending(ctx)
	if err != nil {
		return err
	}
	for _, b := range rows {
		if _, err := p.cfg.Service.Verify(ctx, VerifyArgs{Hostname: b.Hostname}); err != nil {
			if errors.Is(err, ErrBindingNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

func (p *Poller) checkUnhealthy(ctx context.Context) error {
	started := p.cfg.Now()
	defer func() { recordPollerTick("checkUnhealthy", time.Since(started)) }()
	rows, err := p.cfg.Store.ListVerified(ctx)
	if err != nil {
		return err
	}
	for _, b := range rows {
		dnsErr := DualCondition(ctx, p.cfg.Resolver, VerifyInput{
			Hostname:     b.Hostname,
			IsApex:       b.IsApex,
			Token:        b.VerificationToken,
			TunnelTarget: p.cfg.TunnelTarget,
		})
		decision := EvaluateGrace(GraceInput{
			Now:                 p.cfg.Now().UTC(),
			DNSPasses:           dnsErr == nil,
			HealthCheckFailedAt: b.HealthCheckFailedAt,
		})
		switch decision.Action {
		case GraceNoOp, GraceContinue:
			// nothing to update
		case GraceMarkUnhealthy:
			if err := p.cfg.Store.UpdateUnhealthyMark(ctx, b.ID, decision.NewFailedAt); err != nil {
				return err
			}
			recordGraceTransition("marked")
			_ = p.cfg.Auditor.Record(ctx, AuditEvent{
				Action: "domain_unhealthy", Hostname: b.Hostname,
				TeamID: b.TeamID, BindingID: b.ID,
				OccurredAt: p.cfg.Now().UTC(),
			})
		case GraceClearMark:
			if err := p.cfg.Store.UpdateUnhealthyMark(ctx, b.ID, nil); err != nil {
				return err
			}
			recordGraceTransition("cleared")
			_ = p.cfg.Auditor.Record(ctx, AuditEvent{
				Action: "domain_recovered", Hostname: b.Hostname,
				TeamID: b.TeamID, BindingID: b.ID,
				OccurredAt: p.cfg.Now().UTC(),
			})
		case GraceRelease:
			if b.CFHostnameID != "" {
				if err := p.cfg.Cloudflare.DeleteCustomHostname(ctx, b.CFHostnameID); err != nil {
					return err
				}
			}
			if err := p.cfg.Store.UpdateReleased(ctx, b.ID); err != nil {
				return err
			}
			recordGraceTransition("released")
			_ = p.cfg.Auditor.Record(ctx, AuditEvent{
				Action: "domain_released", Hostname: b.Hostname,
				TeamID: b.TeamID, BindingID: b.ID,
				OccurredAt: p.cfg.Now().UTC(),
			})
		}
	}
	return nil
}

func (p *Poller) cleanupExpired(ctx context.Context) error {
	started := p.cfg.Now()
	defer func() { recordPollerTick("cleanupExpired", time.Since(started)) }()
	rows, err := p.cfg.Store.ListExpiredCandidates(ctx)
	if err != nil {
		return err
	}
	now := p.cfg.Now().UTC()
	for _, b := range rows {
		if b.ExpiresAt.After(now) {
			continue
		}
		if err := p.cfg.Store.UpdateExpired(ctx, b.ID); err != nil {
			return err
		}
		recordExpiredCleanup("expired")
		_ = p.cfg.Auditor.Record(ctx, AuditEvent{
			Action: "domain_expired", Hostname: b.Hostname,
			TeamID: b.TeamID, BindingID: b.ID,
			OccurredAt: now,
		})
	}
	return nil
}
