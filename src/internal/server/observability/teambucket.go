// Package observability provides HTTP metrics helpers.
package observability

import (
	"fmt"
	"hash/crc32"
)

// TeamBucketCount is the number of buckets for team distribution (2^6 = 64).
// Chosen for balanced cardinality (log-scale histogram behavior) and CRC32 distribution properties.
const TeamBucketCount = 64

// TeamBucket returns the two-digit bucket ID for a given team ID.
// Uses CRC32(team_id) % 64 for uniform distribution across 64 buckets.
// Returns "anon" if team_id is empty (cross-team or unauthenticated paths).
func TeamBucket(teamID string) string {
	if teamID == "" {
		return "anon"
	}
	h := crc32.ChecksumIEEE([]byte(teamID))
	return fmt.Sprintf("%02d", h%TeamBucketCount)
}
