package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTSignerSign(t *testing.T) {
	t.Parallel()

	key := mustRSAPrivateKey(t)
	signer := NewJWTSigner(12345, key)
	signer.now = func() time.Time { return time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC) }

	tokenString, err := signer.Sign(10 * time.Minute)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	claims := &jwt.RegisteredClaims{}
	parsed, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithTimeFunc(signer.now))
	if err != nil {
		t.Fatalf("ParseWithClaims() error = %v", err)
	}
	if !parsed.Valid {
		t.Fatalf("token is not valid")
	}
	if got := claims.Issuer; got != "12345" {
		t.Fatalf("Issuer = %q", got)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatalf("missing registered claims: %#v", claims)
	}
	if got, want := claims.IssuedAt.Time, time.Date(2026, 5, 11, 23, 59, 30, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("IssuedAt = %s, want %s", got, want)
	}
	if got, want := claims.ExpiresAt.Time, time.Date(2026, 5, 12, 0, 10, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %s, want %s", got, want)
	}
}

func TestLoadRSAPrivateKeyFromFile(t *testing.T) {
	t.Parallel()

	key := mustRSAPrivateKey(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: mustPKCS1Bytes(t, key)})

	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := LoadRSAPrivateKeyFromFile(path)
	if err != nil {
		t.Fatalf("LoadRSAPrivateKeyFromFile() error = %v", err)
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Fatalf("loaded key mismatch")
	}
}

func TestNewJWTSignerFromEnv(t *testing.T) {
	key := mustRSAPrivateKey(t)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: mustPKCS1Bytes(t, key)})

	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("OPS_GITHUB_APP_ID", "98765")
	t.Setenv("OPS_GITHUB_APP_PRIVATE_KEY_PATH", path)

	signer, err := NewJWTSignerFromEnv()
	if err != nil {
		t.Fatalf("NewJWTSignerFromEnv() error = %v", err)
	}
	if signer.appID != 98765 {
		t.Fatalf("appID = %d", signer.appID)
	}
}

func TestParseRSAPrivateKeyPEMRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := ParseRSAPrivateKeyPEM([]byte("not a key")); err == nil {
		t.Fatalf("ParseRSAPrivateKeyPEM() error = nil, want error")
	}
}

func mustRSAPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return key
}

func mustPKCS1Bytes(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()

	return x509.MarshalPKCS1PrivateKey(key)
}
