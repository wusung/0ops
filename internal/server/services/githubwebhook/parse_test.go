package githubwebhook

import "testing"

func TestBranchFromRefHandlesHeadsAndOther(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantOK   bool
	}{
		{"refs/heads/main", "main", true},
		{"refs/heads/feature/x", "feature/x", true},
		{"refs/tags/v1.2.3", "", false},
		{"", "", false},
		{"main", "", false},
	}
	for _, c := range cases {
		name, ok := BranchFromRef(c.in)
		if name != c.wantName || ok != c.wantOK {
			t.Fatalf("BranchFromRef(%q) = %q, %t; want %q, %t", c.in, name, ok, c.wantName, c.wantOK)
		}
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/foo/bar":      "https://github.com/foo/bar",
		"https://github.com/foo/bar/":     "https://github.com/foo/bar",
		"https://github.com/foo/bar.git":  "https://github.com/foo/bar",
		"https://github.com/foo/bar.git/": "https://github.com/foo/bar",
		"  https://github.com/foo/bar  ":  "https://github.com/foo/bar",
	}
	for in, want := range cases {
		if got := NormalizeRepoURL(in); got != want {
			t.Fatalf("NormalizeRepoURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsAcknowledgedEvent(t *testing.T) {
	for _, ev := range []string{EventPush, EventInstallation, EventInstallationRepositories, EventPing} {
		if !IsAcknowledgedEvent(ev) {
			t.Fatalf("IsAcknowledgedEvent(%q) = false, want true", ev)
		}
	}
	for _, ev := range []string{"pull_request", "release", "fork", ""} {
		if IsAcknowledgedEvent(ev) {
			t.Fatalf("IsAcknowledgedEvent(%q) = true, want false", ev)
		}
	}
}

func TestParsePushPayloadDecodesKeyFields(t *testing.T) {
	raw := []byte(`{
		"ref":"refs/heads/main",
		"before":"old","after":"new",
		"deleted":false,
		"repository":{"id":1,"html_url":"https://github.com/foo/bar","default_branch":"main"},
		"installation":{"id":42}
	}`)
	got, err := ParsePushPayload(raw)
	if err != nil {
		t.Fatalf("ParsePushPayload err = %v", err)
	}
	if got.Ref != "refs/heads/main" || got.Installation.ID != 42 || got.Repository.HTMLURL != "https://github.com/foo/bar" {
		t.Fatalf("payload = %+v", got)
	}
}
