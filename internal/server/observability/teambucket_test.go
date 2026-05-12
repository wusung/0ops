package observability

import (
	"fmt"
	"strings"
	"testing"
)

func TestTeamBucket(t *testing.T) {
	tests := []struct {
		name     string
		teamID   string
		wantAnon bool
		wantLen  int
	}{
		{
			name:     "empty team ID returns anon",
			teamID:   "",
			wantAnon: true,
		},
		{
			name:    "valid UUID format returns 2-digit bucket",
			teamID:  "550e8400-e29b-41d4-a716-446655440000",
			wantLen: 2,
		},
		{
			name:    "different team IDs may map to same bucket",
			teamID:  "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TeamBucket(tt.teamID)
			if tt.wantAnon {
				if got != "anon" {
					t.Errorf("TeamBucket(%q) = %q, want anon", tt.teamID, got)
				}
				return
			}
			if len(got) != tt.wantLen {
				t.Errorf("TeamBucket(%q) len = %d, want %d", tt.teamID, len(got), tt.wantLen)
			}
			// Verify it's a valid number
			if _, err := fmt.Sscanf(got, "%02d", new(int)); err != nil {
				t.Errorf("TeamBucket(%q) = %q, want %%02d format: %v", tt.teamID, got, err)
			}
		})
	}
}

// TestTeamBucketDistribution verifies CRC32 distribution is reasonably uniform
func TestTeamBucketDistribution(t *testing.T) {
	buckets := make(map[string]int)
	// Generate team IDs with sequential UUIDs
	for i := 0; i < 1000; i++ {
		teamID := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", i)
		bucket := TeamBucket(teamID)
		buckets[bucket]++
	}

	// With 1000 samples across 64 buckets, each bucket should see ~15.6 entries
	// Allow ±50% variance
	minExpected := 8  // 15.6 * 0.5
	maxExpected := 23 // 15.6 * 1.5

	for bucket, count := range buckets {
		if count < minExpected || count > maxExpected {
			t.Logf("bucket %s has %d entries (expected %d-%d)", bucket, count, minExpected, maxExpected)
		}
	}

	// Verify we use a good fraction of available buckets (should use > 50)
	if len(buckets) < 50 {
		t.Errorf("only used %d/%d buckets, want better distribution", len(buckets), TeamBucketCount)
	}
}

// BenchmarkTeamBucket measures the cost of bucket calculation
func BenchmarkTeamBucket(b *testing.B) {
	teamID := "550e8400-e29b-41d4-a716-446655440000"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = TeamBucket(teamID)
	}
}

// TestTeamBucketConsistency verifies that the same team ID always produces the same bucket
func TestTeamBucketConsistency(t *testing.T) {
	teamID := "550e8400-e29b-41d4-a716-446655440000"
	first := TeamBucket(teamID)

	for i := 0; i < 100; i++ {
		if got := TeamBucket(teamID); got != first {
			t.Errorf("call %d: TeamBucket(%q) = %q, want %q", i, teamID, got, first)
		}
	}
}

// TestTeamBucketFormattedCorrectly verifies format is always "00".."63"
func TestTeamBucketFormattedCorrectly(t *testing.T) {
	for i := 0; i < 100; i++ {
		teamID := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", i)
		bucket := TeamBucket(teamID)

		if !strings.Contains("0123456789", string(bucket[0])) {
			t.Errorf("TeamBucket(%q) first char %c not a digit", teamID, bucket[0])
		}
		if !strings.Contains("0123456789", string(bucket[1])) {
			t.Errorf("TeamBucket(%q) second char %c not a digit", teamID, bucket[1])
		}

		// Verify it's in valid range "00".."63"
		var num int
		if _, err := fmt.Sscanf(bucket, "%02d", &num); err != nil {
			t.Errorf("TeamBucket(%q) = %q, not valid %%02d: %v", teamID, bucket, err)
		}
		if num < 0 || num >= TeamBucketCount {
			t.Errorf("TeamBucket(%q) = %q (num=%d), out of range [0,%d)", teamID, bucket, num, TeamBucketCount)
		}
	}
}
