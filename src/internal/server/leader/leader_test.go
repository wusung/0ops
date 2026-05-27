package leader

import "testing"

func TestAlwaysLeaderIsLeader(t *testing.T) {
	var l Leader = AlwaysLeader{Name: "dev"}
	if !l.IsLeader() {
		t.Fatal("AlwaysLeader.IsLeader() must return true")
	}
}

func TestAlwaysLeaderIdentityNonEmpty(t *testing.T) {
	l := AlwaysLeader{Name: "dev"}
	if l.Identity() == "" {
		t.Fatal("AlwaysLeader.Identity() must be non-empty")
	}
}

func TestAlwaysLeaderIdentityFallsBackWhenNameEmpty(t *testing.T) {
	l := AlwaysLeader{}
	if l.Identity() == "" {
		t.Fatal("AlwaysLeader.Identity() must fall back to a non-empty value")
	}
}
