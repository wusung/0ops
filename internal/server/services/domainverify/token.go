package domainverify

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateVerificationToken returns a 32-byte crypto/rand hex token.
// Hard rule § 15 #5: 32 bytes via crypto/rand, no predictable patterns.
func GenerateVerificationToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
