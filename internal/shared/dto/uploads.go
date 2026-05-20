package dto

import "time"

// UploadResponse is the 201 Created body returned by POST /v1/teams/{team_slug}/uploads.
// Mirrors ingest tree metadata; expires_at is the receipt-time deadline (24h),
// not the pinned deadline (set at confirm time, T13).
type UploadResponse struct {
	UploadID   string    `json:"upload_id"`
	TeamID     string    `json:"team_id"`
	SizeBytes  int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	Format     string    `json:"format"`     // "tar.zst" | "tar.gz"
	ReceivedAt time.Time `json:"received_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
