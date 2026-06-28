// Package sso implements OIDC-first SSO and central revocation (ADR-0016,
// docs/features/sso-saml/spec.md). v1 is OIDC only; SAML columns are reserved
// in the schema but unimplemented (hard rule #10).
package sso

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Discovery holds the OIDC endpoints resolved from the issuer's
// .well-known/openid-configuration document.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Claims are the verified id_token claims 0ops consumes.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Groups        []string
}

// Verifier performs OIDC discovery and id_token verification. It is hermetic:
// the only external surface is httpClient, which tests inject (mock IdP).
//
// v1 verifies RS256 id_tokens (Google Workspace / Okta / Azure AD all default
// to RS256) and fetches JWKS per verification — login frequency is low (≤ once
// per session_max_ttl), so a cache is deferred (spec § 4.2).
type Verifier struct {
	httpClient *http.Client
}

// NewVerifier builds a Verifier. A nil client falls back to a bounded default.
func NewVerifier(client *http.Client) *Verifier {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Verifier{httpClient: client}
}

// Discover fetches and parses the issuer's OIDC discovery document.
func (v *Verifier) Discover(ctx context.Context, issuer string) (Discovery, error) {
	url := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Discovery{}, err
	}
	res, err := v.httpClient.Do(req)
	if err != nil {
		return Discovery{}, fmt.Errorf("oidc discovery: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return Discovery{}, fmt.Errorf("oidc discovery: status %d", res.StatusCode)
	}
	var d Discovery
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		return Discovery{}, fmt.Errorf("oidc discovery decode: %w", err)
	}
	if d.JWKSURI == "" || d.AuthorizationEndpoint == "" {
		return Discovery{}, fmt.Errorf("oidc discovery: incomplete document")
	}
	return d, nil
}

// VerifyIDToken validates an id_token's RS256 signature against the IdP JWKS and
// checks issuer / audience / expiry. groupClaim, if non-empty, names the claim
// carrying the user's IdP groups.
func (v *Verifier) VerifyIDToken(ctx context.Context, disc Discovery, clientID, rawIDToken, groupClaim string) (Claims, error) {
	keys, err := v.fetchJWKS(ctx, disc.JWKSURI)
	if err != nil {
		return Claims{}, err
	}
	keyFunc := func(tok *jwt.Token) (any, error) {
		kid, _ := tok.Header["kid"].(string)
		key, ok := keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown signing key id %q", kid)
		}
		return key, nil
	}

	parsed, err := jwt.Parse(rawIDToken, keyFunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(disc.Issuer),
		jwt.WithAudience(clientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("id_token verify: %w", err)
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, fmt.Errorf("id_token: unexpected claims type")
	}

	out := Claims{}
	if sub, _ := mc["sub"].(string); sub != "" {
		out.Subject = sub
	} else {
		return Claims{}, fmt.Errorf("id_token: missing sub")
	}
	out.Email, _ = mc["email"].(string)
	out.EmailVerified = parseBoolClaim(mc["email_verified"])
	if groupClaim != "" {
		out.Groups = extractGroups(mc[groupClaim])
	}
	return out, nil
}

// parseBoolClaim accepts the OIDC convention where email_verified may arrive as
// a JSON bool or the string "true".
func parseBoolClaim(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(b, "true")
	default:
		return false
	}
}

// VerifyLogoutToken validates an OIDC back-channel logout token: same signature
// / issuer / audience checks as an id_token, but it MUST carry the
// backchannel-logout event and MUST NOT carry a nonce (OIDC Back-Channel Logout
// § 2.4). This stops a replayed id_token from being used to force-deprovision a
// user (spec § 7.1). Returns the subject to revoke.
func (v *Verifier) VerifyLogoutToken(ctx context.Context, disc Discovery, clientID, rawLogoutToken string) (string, error) {
	keys, err := v.fetchJWKS(ctx, disc.JWKSURI)
	if err != nil {
		return "", err
	}
	keyFunc := func(tok *jwt.Token) (any, error) {
		kid, _ := tok.Header["kid"].(string)
		key, ok := keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown signing key id %q", kid)
		}
		return key, nil
	}
	parsed, err := jwt.Parse(rawLogoutToken, keyFunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(disc.Issuer),
		jwt.WithAudience(clientID),
	)
	if err != nil {
		return "", fmt.Errorf("logout_token verify: %w", err)
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("logout_token: unexpected claims type")
	}
	if _, hasNonce := mc["nonce"]; hasNonce {
		return "", fmt.Errorf("logout_token: nonce is prohibited")
	}
	if !hasBackchannelLogoutEvent(mc["events"]) {
		return "", fmt.Errorf("logout_token: missing backchannel-logout event")
	}
	sub, _ := mc["sub"].(string)
	if sub == "" {
		return "", fmt.Errorf("logout_token: missing sub")
	}
	return sub, nil
}

const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

func hasBackchannelLogoutEvent(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, present := m[backchannelLogoutEvent]
	return present
}

func extractGroups(v any) []string {
	switch g := v.(type) {
	case []string:
		return g
	case []any:
		out := make([]string, 0, len(g))
		for _, item := range g {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if g == "" {
			return nil
		}
		return []string{g}
	default:
		return nil
	}
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) fetchJWKS(ctx context.Context, jwksURI string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	res, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks fetch: status %d", res.StatusCode)
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("jwks decode: %w", err)
	}
	out := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAJWK(k)
		if err != nil {
			return nil, err
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("jwks: no usable RSA keys")
	}
	return out, nil
}

func parseRSAJWK(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("jwk exponent: %w", err)
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() <= 0 {
		return nil, fmt.Errorf("jwk exponent out of range")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(e.Int64()),
	}, nil
}
