package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestSignMatchesReceiverRecomputation(t *testing.T) {
	secret := []byte("super-secret-key")
	ts := "1717243200"
	body := []byte(`{"event":"app.deleted"}`)

	got := Sign(secret, ts, body)

	// Receiver recomputes HMAC over timestamp + "." + body.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("Sign = %q, want %q", got, want)
	}
}

func TestSignChangesWithTimestamp(t *testing.T) {
	secret := []byte("k")
	body := []byte("b")
	if Sign(secret, "1", body) == Sign(secret, "2", body) {
		t.Fatal("signature must bind the timestamp (replay guard)")
	}
}

func TestSignedHeadersComplete(t *testing.T) {
	secret := []byte("k")
	body := []byte(`{"x":1}`)
	ts := time.Unix(1717243200, 0).UTC()
	h := SignedHeaders(secret, "app.deleted", "del-9", ts, body)

	for _, k := range []string{"Content-Type", HeaderEvent, HeaderDelivery, HeaderTimestamp, HeaderSignature} {
		if h[k] == "" {
			t.Errorf("missing header %q", k)
		}
	}
	if h[HeaderTimestamp] != "1717243200" {
		t.Errorf("timestamp header = %q", h[HeaderTimestamp])
	}
	if h[HeaderSignature] != Sign(secret, "1717243200", body) {
		t.Errorf("signature header mismatch")
	}
	if h[HeaderDelivery] != "del-9" || h[HeaderEvent] != "app.deleted" {
		t.Errorf("delivery/event headers wrong: %v", h)
	}
}
