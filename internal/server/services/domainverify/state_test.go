package domainverify

import "testing"

func TestStatusTransitionsAllowed(t *testing.T) {
	t.Parallel()
	cases := []struct{ from, to Status }{
		{StatusPending, StatusVerified},
		{StatusPending, StatusExpired},
		{StatusVerified, StatusUnhealthy},
		{StatusUnhealthy, StatusVerified},
		{StatusUnhealthy, StatusReleased},
		{StatusVerified, StatusReleased},
		{StatusPending, StatusReleased},
	}
	for _, c := range cases {
		t.Run(string(c.from)+"->"+string(c.to), func(t *testing.T) {
			if !CanTransition(c.from, c.to) {
				t.Fatalf("expected %s -> %s allowed", c.from, c.to)
			}
		})
	}
}

func TestStatusTransitionsRejected(t *testing.T) {
	t.Parallel()
	cases := []struct{ from, to Status }{
		{StatusExpired, StatusVerified},
		{StatusReleased, StatusVerified},
		{StatusReleased, StatusPending},
		{StatusVerified, StatusPending},
	}
	for _, c := range cases {
		t.Run(string(c.from)+"->"+string(c.to), func(t *testing.T) {
			if CanTransition(c.from, c.to) {
				t.Fatalf("expected %s -> %s rejected", c.from, c.to)
			}
		})
	}
}

func TestStatusKnown(t *testing.T) {
	t.Parallel()
	for _, s := range []Status{StatusPending, StatusVerified, StatusUnhealthy, StatusExpired, StatusReleased} {
		if !s.Known() {
			t.Fatalf("%s should be Known()", s)
		}
	}
	if Status("garbage").Known() {
		t.Fatal("garbage should not be Known()")
	}
}
