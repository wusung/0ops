package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/db"
)

func TestNextBackoffMatchesSpec(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 60 * time.Second},
		{1, 2 * time.Minute},
		{2, 4 * time.Minute},
		{3, 8 * time.Minute},
		{4, 16 * time.Minute},
		{5, 30 * time.Minute}, // 60 * 2^5 = 32min capped at 30min
		{6, 30 * time.Minute},
		{10, 30 * time.Minute},
		{-3, 60 * time.Second}, // negative defaults to 0
	}
	for _, tc := range cases {
		got := NextBackoff(tc.attempts)
		if got != tc.want {
			t.Fatalf("NextBackoff(%d) = %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestShouldFailPermanently(t *testing.T) {
	if ShouldFailPermanently(8) {
		t.Fatalf("ShouldFailPermanently(8) = true, want false (8 is the cap)")
	}
	if !ShouldFailPermanently(9) {
		t.Fatalf("ShouldFailPermanently(9) = false, want true")
	}
}

func TestHandlerRegistryRegisterAndLookup(t *testing.T) {
	reg := NewHandlerRegistry()
	reg.Register("kind_a", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome { return HandlerOutcome{Completed: true} }))
	if _, ok := reg.Lookup("kind_a"); !ok {
		t.Fatalf("Lookup(kind_a) = false, want true")
	}
	if _, ok := reg.Lookup("kind_b"); ok {
		t.Fatalf("Lookup(kind_b) = true, want false")
	}
}

func TestHandlerRegistryDuplicateRegistrationPanics(t *testing.T) {
	reg := NewHandlerRegistry()
	reg.Register("kind_a", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome { return HandlerOutcome{} }))
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatalf("expected duplicate registration to panic")
		}
	}()
	reg.Register("kind_a", HandlerFunc(func(_ context.Context, _ db.ReconciliationJobRow) HandlerOutcome { return HandlerOutcome{} }))
}
