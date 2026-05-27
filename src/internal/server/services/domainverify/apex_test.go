package domainverify

import "testing"

func TestDetectApex(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"example.com":       true,
		"foo.co.uk":         true,
		"app.example.com":   false,
		"a.b.example.com":   false,
		"example.co.uk":     true,
		"www.example.co.uk": false,
	}
	for host, want := range cases {
		t.Run(host, func(t *testing.T) {
			got, err := DetectApex(host)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestDetectApexRejectsInvalid(t *testing.T) {
	t.Parallel()
	if _, err := DetectApex(""); err == nil {
		t.Fatal("expected error for empty hostname")
	}
}
