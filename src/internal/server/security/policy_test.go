package security

import "testing"

func TestResolvePATTTLDays(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		policy    TTLPolicy
		want      int
	}{
		{"zero requested falls to default", 0, TTLPolicy{}, DefaultPATTTLDays},
		{"negative requested falls to default", -5, TTLPolicy{}, DefaultPATTTLDays},
		{"requested within global max passes", 90, TTLPolicy{}, 90},
		{"requested above global max is clamped", 400, TTLPolicy{}, GlobalMaxPATTTLDays},
		{"team cap tightens below request", 90, TTLPolicy{MaxPATTTLDays: 30}, 30},
		{"team cap only tightens, request below cap wins", 14, TTLPolicy{MaxPATTTLDays: 30}, 14},
		{"team cap above global is itself clamped to global", 400, TTLPolicy{MaxPATTTLDays: 1000}, GlobalMaxPATTTLDays},
		{"team cap unset uses global", 200, TTLPolicy{}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePATTTLDays(tc.requested, tc.policy); got != tc.want {
				t.Fatalf("ResolvePATTTLDays(%d, %+v) = %d, want %d", tc.requested, tc.policy, got, tc.want)
			}
		})
	}
}

func TestResolveDeviceTTLDays(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		policy    TTLPolicy
		want      int
	}{
		{"zero requested falls to global device default", 0, TTLPolicy{}, GlobalMaxDeviceTTLDays},
		{"requested above global is clamped", 60, TTLPolicy{}, GlobalMaxDeviceTTLDays},
		{"team cap tightens below request", 30, TTLPolicy{MaxDeviceTTLDays: 14}, 14},
		{"team cap only tightens", 7, TTLPolicy{MaxDeviceTTLDays: 14}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveDeviceTTLDays(tc.requested, tc.policy); got != tc.want {
				t.Fatalf("ResolveDeviceTTLDays(%d, %+v) = %d, want %d", tc.requested, tc.policy, got, tc.want)
			}
		})
	}
}
