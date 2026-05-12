package githubapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrMissingAppID      = errors.New("missing github app id")
	ErrMissingPrivateKey = errors.New("missing github app private key")
	ErrInvalidPrivateKey = errors.New("invalid github app private key")
)

type JWTSigner struct {
	appID      int64
	privateKey *rsa.PrivateKey
	now        func() time.Time
}

func NewJWTSigner(appID int64, privateKey *rsa.PrivateKey) *JWTSigner {
	return &JWTSigner{
		appID:      appID,
		privateKey: privateKey,
		now:        time.Now,
	}
}

func NewJWTSignerFromEnv() (*JWTSigner, error) {
	appIDRaw := strings.TrimSpace(os.Getenv("OPS_GITHUB_APP_ID"))
	if appIDRaw == "" {
		return nil, ErrMissingAppID
	}

	appID, err := strconv.ParseInt(appIDRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse OPS_GITHUB_APP_ID: %w", err)
	}

	keyPath := strings.TrimSpace(os.Getenv("OPS_GITHUB_APP_PRIVATE_KEY_PATH"))
	if keyPath == "" {
		return nil, ErrMissingPrivateKey
	}

	privateKey, err := LoadRSAPrivateKeyFromFile(keyPath)
	if err != nil {
		return nil, err
	}
	return NewJWTSigner(appID, privateKey), nil
}

func LoadRSAPrivateKeyFromFile(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	return ParseRSAPrivateKeyPEM(data)
}

func ParseRSAPrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, ErrInvalidPrivateKey
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrInvalidPrivateKey
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, ErrInvalidPrivateKey
	}
	return key, nil
}

func (s *JWTSigner) Sign(ttl time.Duration) (string, error) {
	if s == nil || s.privateKey == nil {
		return "", ErrMissingPrivateKey
	}

	now := s.now()
	claims := jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(s.appID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(s.privateKey)
}
