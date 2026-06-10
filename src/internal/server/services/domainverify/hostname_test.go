package domainverify

import (
	"strings"
	"testing"

	opsruntime "github.com/winshare/zeroops/internal/shared/runtime"
)

func TestValidateHostnameAccepts(t *testing.T) {
	t.Parallel()
	cases := []string{
		"example.com",
		"app.example.com",
		"a.b.c.example.com",
		"foo-bar.example.com",
		"x.io",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			if err := ValidateHostname(h); err != nil {
				t.Fatalf("unexpected: %v", err)
			}
		})
	}
}

func TestValidateHostnameRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"empty":                "",
		"upper":                "Example.COM",
		"label_too_long":       strings.Repeat("a", 64) + ".com",
		"hostname_too_long":    strings.Repeat("a.", 127) + "co",
		"leading_hyphen":       "-foo.example.com",
		"trailing_hyphen":      "foo-.example.com",
		"underscore":           "foo_bar.example.com",
		"reserved_suffix":      "demo." + opsruntime.DomainBase(),
		"reserved_suffix_apex": opsruntime.DomainBase(),
		"trailing_dot":         "example.com.",
		"space":                "ex ample.com",
		"single_label":         "localhost",
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateHostname(h); err == nil {
				t.Fatalf("expected error for %q", h)
			}
		})
	}
}

func TestValidateHostnameReservedSuffixError(t *testing.T) {
	t.Parallel()
	err := ValidateHostname("demo." + opsruntime.DomainBase())
	if err == nil || !isReservedSuffixErr(err) {
		t.Fatalf("expected ReservedSuffix error, got %v", err)
	}
}
