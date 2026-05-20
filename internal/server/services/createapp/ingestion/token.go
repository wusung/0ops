package ingestion

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ScopeDownloadUpload is the only scope accepted by Verify().
// Sign() forces this value regardless of caller input — callers cannot mint
// tokens with arbitrary scopes through this signer.
const ScopeDownloadUpload = "download-upload"

const (
	issuerOps     = "0ops"
	audienceGHA   = "gha-build"
	subjectPrefix = "upload:"
)

// TokenClaims carries the fields the workflow needs to fetch the right
// archive and that the server checks on download. Embeds jwt.RegisteredClaims
// for standard exp/iat/iss/aud/sub handling.
type TokenClaims struct {
	TeamID      string `json:"team_id"`
	UploadID    string `json:"upload_id"`
	DeployRunID string `json:"deploy_run_id"`
	Scope       string `json:"scope"`
	jwt.RegisteredClaims
}

// TokenSigner mints and verifies short-lived JWTs for the GHA upload-fetch
// path. The TTL value applies to both signing (sets exp) and verifying
// (zero-tolerance on expiry).
type TokenSigner struct {
	Secret []byte
	TTL    time.Duration
}

// Sentinel errors. Callers should distinguish these to map to HTTP responses:
//
//	ErrTokenInvalid       -> 401 unauthorized
//	ErrTokenExpired       -> 401 unauthorized (different metric path)
//	ErrTokenScopeMismatch -> 403 forbidden (token mismatch on a valid token)
var (
	ErrTokenInvalid       = errors.New("invalid token")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenScopeMismatch = errors.New("token scope mismatch")
)

// Sign returns a signed token containing the supplied claims. The Scope
// field on the input is ignored; the constant ScopeDownloadUpload is set
// instead. IssuedAt and ExpiresAt are derived from s.TTL.
func (s *TokenSigner) Sign(c TokenClaims) (string, error) {
	if len(s.Secret) == 0 {
		return "", errors.New("ingestion: token signer has empty secret")
	}
	now := time.Now().UTC()
	c.Scope = ScopeDownloadUpload // always forced
	c.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    issuerOps,
		Audience:  jwt.ClaimStrings{audienceGHA},
		Subject:   subjectPrefix + c.UploadID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)), // clock skew
		ExpiresAt: jwt.NewNumericDate(now.Add(s.TTL)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return token.SignedString(s.Secret)
}

// Verify parses and validates the token: signature (HMAC-SHA256 with s.Secret),
// expiry, audience (gha-build), issuer (0ops), and scope (download-upload).
// Returns the decoded claims on success.
func (s *TokenSigner) Verify(raw string) (TokenClaims, error) {
	var claims TokenClaims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(t *jwt.Token) (any, error) {
			// CRITICAL: enforce HS256 — reject any token claiming a different alg.
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, ErrTokenInvalid
			}
			return s.Secret, nil
		},
		jwt.WithAudience(audienceGHA),
		jwt.WithIssuer(issuerOps),
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired):
			return TokenClaims{}, ErrTokenExpired
		default:
			return TokenClaims{}, ErrTokenInvalid
		}
	}
	if claims.Scope != ScopeDownloadUpload {
		return TokenClaims{}, ErrTokenScopeMismatch
	}
	return claims, nil
}
