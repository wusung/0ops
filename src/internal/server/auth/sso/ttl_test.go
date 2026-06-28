package sso

import (
	"testing"
	"time"
)

func TestSSOTokenExpiry(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		idpExpiry  time.Time
		maxTTLSecs int
		want       time.Time
	}{
		{"default 8h", time.Time{}, 28800, now.Add(8 * time.Hour)},
		{"cap at 24h when configured above", time.Time{}, 48 * 3600, now.Add(24 * time.Hour)},
		{"cap at 24h when zero", time.Time{}, 0, now.Add(24 * time.Hour)},
		{"idp session shorter wins", now.Add(time.Hour), 28800, now.Add(time.Hour)},
		{"idp session longer ignored", now.Add(40 * time.Hour), 28800, now.Add(8 * time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SSOTokenExpiry(now, tc.idpExpiry, tc.maxTTLSecs)
			if !got.Equal(tc.want) {
				t.Fatalf("SSOTokenExpiry = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSSOTokenExpiryNeverExceeds24h(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	got := SSOTokenExpiry(now, time.Time{}, 1_000_000)
	if got.Sub(now) > SSOTokenMaxTTL {
		t.Fatalf("expiry %v exceeds 24h ceiling", got.Sub(now))
	}
}
