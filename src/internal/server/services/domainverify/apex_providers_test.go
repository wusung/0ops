package domainverify

import (
	"strings"
	"testing"
)

func TestIncompatibleApexProvidersNotEmpty(t *testing.T) {
	t.Parallel()
	got := IncompatibleApexProviders()
	if len(got) == 0 {
		t.Fatal("expected at least one provider entry")
	}
	for _, p := range got {
		if p.Name == "" || p.Reason == "" || p.Alternative == "" {
			t.Fatalf("incomplete entry: %+v", p)
		}
	}
}

func TestIncompatibleApexProvidersIncludesKnownNames(t *testing.T) {
	t.Parallel()
	names := make(map[string]bool)
	for _, p := range IncompatibleApexProviders() {
		names[strings.ToLower(p.Name)] = true
	}
	for _, want := range []string{"godaddy", "azure"} {
		found := false
		for n := range names {
			if strings.Contains(n, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected provider list to mention %q", want)
		}
	}
}
