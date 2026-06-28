package notify

import (
	"errors"
	"net"
	"testing"
)

func staticResolver(ips ...string) func(string) ([]net.IP, error) {
	return func(string) ([]net.IP, error) {
		out := make([]net.IP, 0, len(ips))
		for _, s := range ips {
			out = append(out, net.ParseIP(s))
		}
		return out, nil
	}
}

func TestValidateWebhookURLAcceptsPublicHTTPS(t *testing.T) {
	err := ValidateWebhookURL("https://hooks.example.com/path", staticResolver("93.184.216.34"))
	if err != nil {
		t.Fatalf("public https rejected: %v", err)
	}
}

func TestValidateWebhookURLRejectsNonHTTPS(t *testing.T) {
	for _, raw := range []string{"http://hooks.example.com", "ftp://x", "https://"} {
		if err := ValidateWebhookURL(raw, staticResolver("93.184.216.34")); !errors.Is(err, ErrInvalidWebhookURL) {
			t.Errorf("ValidateWebhookURL(%q) err = %v, want ErrInvalidWebhookURL", raw, err)
		}
	}
}

func TestValidateWebhookURLRejectsMetadataAndPrivate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"metadata literal", "https://169.254.169.254/latest/meta-data"},
		{"loopback literal", "https://127.0.0.1/hook"},
		{"private literal", "https://10.0.0.5/hook"},
		{"link-local literal", "https://169.254.1.1/hook"},
		{"private via dns", "https://internal.example.com/hook"},
	}
	resolvePrivate := staticResolver("10.1.2.3")
	for _, tc := range cases {
		resolve := resolvePrivate
		if net.ParseIP(hostOf(tc.raw)) != nil {
			resolve = nil // literal IP, no DNS needed
		}
		if err := ValidateWebhookURL(tc.raw, resolve); !errors.Is(err, ErrInvalidWebhookURL) {
			t.Errorf("%s: err = %v, want ErrInvalidWebhookURL", tc.name, err)
		}
	}
}

func TestValidateWebhookURLRejectsCGNATAndNAT64(t *testing.T) {
	cases := []struct{ name, ip string }{
		{"cgnat", "100.64.1.2"},
		{"nat64 embedding loopback", "64:ff9b::7f00:1"},
		{"ipv4-mapped loopback", "::ffff:127.0.0.1"},
		{"ipv6 ULA", "fc00::1"},
	}
	for _, tc := range cases {
		if err := ValidateWebhookURL("https://internal.example.com/h", staticResolver(tc.ip)); !errors.Is(err, ErrInvalidWebhookURL) {
			t.Errorf("%s (%s): err = %v, want ErrInvalidWebhookURL", tc.name, tc.ip, err)
		}
	}
}

func TestValidateWebhookURLRejectsMixedDNSAnswers(t *testing.T) {
	// One public + one private answer must reject (DNS-rebinding gap).
	err := ValidateWebhookURL("https://rebind.example.com/hook", staticResolver("93.184.216.34", "10.0.0.9"))
	if !errors.Is(err, ErrInvalidWebhookURL) {
		t.Fatalf("mixed answers err = %v, want ErrInvalidWebhookURL", err)
	}
}

func hostOf(raw string) string {
	// tiny helper for the table above; mirrors url.Hostname for literals.
	for i := 0; i < len(raw); i++ {
		if raw[i] == '/' && i+1 < len(raw) && raw[i+1] == '/' {
			rest := raw[i+2:]
			for j := 0; j < len(rest); j++ {
				if rest[j] == '/' || rest[j] == ':' {
					return rest[:j]
				}
			}
			return rest
		}
	}
	return ""
}
