package cli

import "testing"

func TestAlreadyLoggedInFalseWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if alreadyLoggedIn("https://api.example.com") {
		t.Errorf("expected false on missing auth.json")
	}
}
