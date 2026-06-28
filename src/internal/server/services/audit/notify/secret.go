package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

// signingKeyBytes is the generated signing-key length (spec § 6.1: ≥ 32 byte).
const signingKeyBytes = 32

// GenerateSigningKey returns a fresh random signing key as raw bytes plus its
// base64 (write-only reveal) encoding. The plaintext is returned exactly once
// on create / rotate and then only the at-rest form is kept (spec § 6.1 / § 8).
func GenerateSigningKey() (raw []byte, b64 string, err error) {
	buf := make([]byte, signingKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	return buf, base64.StdEncoding.EncodeToString(buf), nil
}

// DecodeSigningKey parses a base64 signing key back to raw bytes for HMAC use.
func DecodeSigningKey(b64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(b64)
}

// SecretStore is the at-rest seam for per-subscription signing keys (spec § 8).
//
// DEFERRED: the production implementation does envelope encryption via the
// secrets-management module, which is not yet in the repo. The v1 store
// (db.webhookSecretStore) persists the base64 key in webhook_subscription and
// is the documented swap point — it is NOT yet at-rest encrypted. The key is
// still never exposed by any GET path (write-only reveal) and never enters
// log / audit / metric / payload.
type SecretStore interface {
	// Resolve returns the raw signing-key bytes for a subscription's secret_ref.
	Resolve(ctx context.Context, secretRef string) ([]byte, error)
}

// ErrSecretNotFound is returned when a secret_ref does not resolve.
var ErrSecretNotFound = errors.New("notify: signing key not found")
