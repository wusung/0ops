package auth

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashBearerToken produces the stored token hash used by the dev auth chain.
func HashBearerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}
