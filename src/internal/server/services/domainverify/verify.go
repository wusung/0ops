package domainverify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Resolver abstracts DNS lookups used by DualCondition. spec § 6.3.
type Resolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupTXT(ctx context.Context, host string) ([]string, error)
}

// VerifyInput is the immutable input for one DNS verification attempt.
type VerifyInput struct {
	Hostname     string
	IsApex       bool
	Token        string
	TunnelTarget string // e.g. "tunnel-abc.cfargotunnel.com" (no trailing dot)
}

// ErrCNAMENotMatched reports that the hostname's CNAME / A record does not
// resolve to the tunnel target.
var ErrCNAMENotMatched = errors.New("cname not matched")

// ErrTXTNotMatched reports that the `_0ops-verify.<host>` TXT record is
// absent or does not contain the verification token.
var ErrTXTNotMatched = errors.New("txt not matched")

// DualCondition runs the spec § 6.3 dual-condition DNS check.
// Hard rule § 15 #6: both conditions must pass; never CNAME-only.
func DualCondition(ctx context.Context, r Resolver, in VerifyInput) error {
	if err := dnsCheckCNAME(ctx, r, in); err != nil {
		return err
	}
	return dnsCheckTXT(ctx, r, in)
}

func dnsCheckCNAME(ctx context.Context, r Resolver, in VerifyInput) error {
	target := strings.TrimSuffix(strings.ToLower(in.TunnelTarget), ".")
	if in.IsApex {
		hostIPs, err := r.LookupHost(ctx, in.Hostname)
		if err != nil {
			return fmt.Errorf("lookup host %s: %w", in.Hostname, err)
		}
		tunnelIPs, err := r.LookupHost(ctx, in.TunnelTarget)
		if err != nil {
			return fmt.Errorf("lookup tunnel %s: %w", in.TunnelTarget, err)
		}
		if !intersects(hostIPs, tunnelIPs) {
			return ErrCNAMENotMatched
		}
		return nil
	}
	cname, err := r.LookupCNAME(ctx, in.Hostname)
	if err != nil {
		return fmt.Errorf("lookup CNAME %s: %w", in.Hostname, err)
	}
	cname = strings.TrimSuffix(strings.ToLower(cname), ".")
	if cname != target && !strings.HasSuffix(cname, "."+target) {
		return ErrCNAMENotMatched
	}
	return nil
}

func dnsCheckTXT(ctx context.Context, r Resolver, in VerifyInput) error {
	records, err := r.LookupTXT(ctx, "_0ops-verify."+in.Hostname)
	if err != nil {
		return fmt.Errorf("lookup TXT %s: %w", in.Hostname, err)
	}
	if !slices.Contains(records, in.Token) {
		return ErrTXTNotMatched
	}
	return nil
}

func intersects(a, b []string) bool {
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	for _, v := range a {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}
