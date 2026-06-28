package notify

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ErrInvalidWebhookURL is returned when a subscription URL fails the SSRF /
// scheme guard. The handler maps it to 422 webhook_url_invalid (spec § 6.4,
// hard rule #8).
var ErrInvalidWebhookURL = errors.New("webhook url invalid")

// ValidateWebhookURL enforces the spec § 6.4 destination rules: https only, a
// resolvable host, and rejection of private / loopback / link-local / multicast
// / unspecified / metadata (169.254.169.254) targets. resolve is injectable so
// tests need no real DNS; nil uses net.LookupIP.
//
// A literal-IP host is checked directly; a hostname is resolved and EVERY
// returned address must pass (a single private answer rejects the URL, closing
// the DNS-rebinding gap at config time).
func ValidateWebhookURL(raw string, resolve func(host string) ([]net.IP, error)) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ErrInvalidWebhookURL
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return ErrInvalidWebhookURL
	}
	host := u.Hostname()
	if host == "" {
		return ErrInvalidWebhookURL
	}

	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return ErrInvalidWebhookURL
		}
		return nil
	}

	if resolve == nil {
		resolve = net.LookupIP
	}
	ips, err := resolve(host)
	if err != nil || len(ips) == 0 {
		return ErrInvalidWebhookURL
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return ErrInvalidWebhookURL
		}
	}
	return nil
}

// metadataIP is the cloud instance-metadata endpoint (link-local already covers
// it, but it is called out explicitly per spec § 6.4).
var metadataIP = net.ParseIP("169.254.169.254")

// extraBlockedNets covers ranges Go's stdlib predicates miss but which still
// reach internal/shared infrastructure: RFC 6598 CGNAT (100.64.0.0/10) and the
// well-known NAT64 prefix (64:ff9b::/96, which can embed a private IPv4).
var extraBlockedNets = mustCIDRs("100.64.0.0/10", "64:ff9b::/96")

// isPublicIP reports whether ip is a routable public address — i.e. not
// loopback, private, link-local, multicast, unspecified, the metadata IP, or a
// CGNAT / NAT64 range.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.Equal(metadataIP) {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, n := range extraBlockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

func mustCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("notify: bad CIDR " + c)
		}
		out = append(out, n)
	}
	return out
}
