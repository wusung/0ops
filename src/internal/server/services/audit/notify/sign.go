package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Outbound signature headers (spec § 6.3). 0ops is the signer here (the
// direction is reversed from the inbound GitHub webhook path).
const (
	HeaderEvent     = "X-0ops-Event"
	HeaderDelivery  = "X-0ops-Delivery"
	HeaderTimestamp = "X-0ops-Timestamp"
	HeaderSignature = "X-0ops-Signature-256"
)

// Sign computes the spec § 6.3 signature over `timestamp + "." + body`. Binding
// the timestamp into the HMAC lets the receiver reject stale requests (replay
// guard). Returns the `sha256=<hex>` header value.
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// SignedHeaders returns the full header set for one delivery POST. ts is the
// signing time; secret is the per-subscription key. Every delivery carries all
// four headers — never an unsigned request (hard rule #6).
func SignedHeaders(secret []byte, eventKey, deliveryID string, ts time.Time, body []byte) map[string]string {
	timestamp := strconv.FormatInt(ts.UTC().Unix(), 10)
	return map[string]string{
		"Content-Type":  "application/json",
		HeaderEvent:     eventKey,
		HeaderDelivery:  deliveryID,
		HeaderTimestamp: timestamp,
		HeaderSignature: Sign(secret, timestamp, body),
	}
}
