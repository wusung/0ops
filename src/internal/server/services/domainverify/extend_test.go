package domainverify

import (
	"errors"
	"testing"
	"time"
)

func TestExtendApplyFirstAddsTwentyFourHours(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(2 * time.Hour)
	got, err := ApplyExtend(ExtendInput{
		Now:         now,
		Verified:    false,
		ExtendsUsed: 0,
		ExpiresAt:   expiry,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := expiry.Add(24 * time.Hour)
	if !got.NewExpiresAt.Equal(want) {
		t.Fatalf("got expires=%s, want %s", got.NewExpiresAt, want)
	}
	if got.NewExtendsUsed != 1 {
		t.Fatalf("got used=%d, want 1", got.NewExtendsUsed)
	}
}

func TestExtendApplySecondPermitted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	out, err := ApplyExtend(ExtendInput{
		Now:         now,
		Verified:    false,
		ExtendsUsed: 1,
		ExpiresAt:   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out.NewExtendsUsed != 2 {
		t.Fatalf("got %d, want 2", out.NewExtendsUsed)
	}
}

func TestExtendApplyThirdRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	_, err := ApplyExtend(ExtendInput{
		Now:         now,
		Verified:    false,
		ExtendsUsed: 2,
		ExpiresAt:   now.Add(time.Hour),
	})
	if !errors.Is(err, ErrCannotExtend) {
		t.Fatalf("got %v, want ErrCannotExtend", err)
	}
}

func TestExtendApplyRejectsAfterExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	_, err := ApplyExtend(ExtendInput{
		Now:         now,
		Verified:    false,
		ExtendsUsed: 0,
		ExpiresAt:   now.Add(-time.Hour),
	})
	if !errors.Is(err, ErrCannotExtend) {
		t.Fatalf("got %v, want ErrCannotExtend (expired)", err)
	}
}

func TestExtendApplyRejectsVerified(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	_, err := ApplyExtend(ExtendInput{
		Now:         now,
		Verified:    true,
		ExtendsUsed: 0,
		ExpiresAt:   now.Add(time.Hour),
	})
	if !errors.Is(err, ErrCannotExtend) {
		t.Fatalf("got %v, want ErrCannotExtend (already verified)", err)
	}
}
