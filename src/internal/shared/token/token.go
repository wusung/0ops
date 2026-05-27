// Package token provides token utilities.
package token

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	tokenPrefixDevice = "op_dev_"
	tokenPrefixPAT    = "op_pat_"

	argon2MemoryKiB  = 64 * 1024
	argon2Iterations = 3
	argon2Parallel   = 2
	argon2SaltSize   = 16
	argon2KeySize    = 32
)

// ParsedBearerToken describes the structured bearer token format.
type ParsedBearerToken struct {
	Kind   string
	ID     string
	Secret string
}

// NewBearerToken generates a bearer token for the given kind and row ID.
func NewBearerToken(kind, id string) (string, error) {
	secret, err := NewBearerTokenSecret()
	if err != nil {
		return "", err
	}
	return FormatBearerToken(kind, id, secret), nil
}

// NewBearerTokenSecret generates the secret suffix used in bearer tokens.
func NewBearerTokenSecret() (string, error) {
	return randomTokenSecret()
}

// FormatBearerToken formats a bearer token from its parts.
func FormatBearerToken(kind, id, secret string) string {
	return fmt.Sprintf("%s%s.%s", tokenPrefix(kind), id, secret)
}

// ParseBearerToken extracts the kind, row ID and secret from a bearer token.
func ParseBearerToken(token string) (ParsedBearerToken, error) {
	switch {
	case strings.HasPrefix(token, tokenPrefixDevice):
		return parseBearerToken(token, "device", tokenPrefixDevice)
	case strings.HasPrefix(token, tokenPrefixPAT):
		return parseBearerToken(token, "pat", tokenPrefixPAT)
	default:
		return ParsedBearerToken{}, errors.New("invalid bearer token prefix")
	}
}

// HashBearerToken produces the stored argon2id hash for a token secret.
func HashBearerToken(secret string) (string, error) {
	salt := make([]byte, argon2SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt, argon2Iterations, argon2MemoryKiB, argon2Parallel, argon2KeySize)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2MemoryKiB,
		argon2Iterations,
		argon2Parallel,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// CompareBearerToken verifies a token secret against the stored argon2id hash.
func CompareBearerToken(secret, encodedHash string) (bool, error) {
	memory, iterations, parallel, salt, want, err := decodeArgon2Hash(encodedHash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(secret), salt, iterations, memory, parallel, uint32(len(want))) //nolint:gosec // len(want) fits in uint32
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func tokenPrefix(kind string) string {
	switch kind {
	case "device":
		return tokenPrefixDevice
	case "pat":
		return tokenPrefixPAT
	default:
		return ""
	}
}

func parseBearerToken(token, kind, prefix string) (ParsedBearerToken, error) {
	remainder := strings.TrimPrefix(token, prefix)
	id, secret, ok := strings.Cut(remainder, ".")
	if !ok || strings.TrimSpace(id) == "" || strings.TrimSpace(secret) == "" {
		return ParsedBearerToken{}, errors.New("invalid bearer token format")
	}
	return ParsedBearerToken{Kind: kind, ID: id, Secret: secret}, nil
}

func randomTokenSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeArgon2Hash(encodedHash string) (memory uint32, iterations uint32, parallel uint8, salt []byte, key []byte, err error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2 hash format")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallel); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("invalid argon2 params: %w", err)
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("invalid argon2 salt: %w", err)
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("invalid argon2 key: %w", err)
	}
	return memory, iterations, parallel, salt, key, nil
}
