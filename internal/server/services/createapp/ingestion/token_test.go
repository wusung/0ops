package ingestion

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTokenSigner_SignAndVerifyRoundtrip(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("test-secret-please-rotate"), TTL: 15 * time.Minute}
	tok, err := signer.Sign(TokenClaims{
		TeamID: "team_a", UploadID: "upl_x", DeployRunID: "run_1",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.HasPrefix(tok, "ey") { // any JWT starts with "ey" (base64 of "{")
		t.Fatalf("token doesn't look like a JWT: %q", tok)
	}

	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.TeamID != "team_a" {
		t.Fatalf("TeamID: got %q want %q", claims.TeamID, "team_a")
	}
	if claims.UploadID != "upl_x" {
		t.Fatalf("UploadID: got %q want %q", claims.UploadID, "upl_x")
	}
	if claims.DeployRunID != "run_1" {
		t.Fatalf("DeployRunID: got %q want %q", claims.DeployRunID, "run_1")
	}
	if claims.Scope != ScopeDownloadUpload {
		t.Fatalf("Scope: got %q want %q", claims.Scope, ScopeDownloadUpload)
	}
}

func TestTokenSigner_SignForcesDownloadUploadScope(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("s"), TTL: time.Hour}
	tok, _ := signer.Sign(TokenClaims{
		TeamID: "t", UploadID: "u", DeployRunID: "r",
		Scope: "i-tried-to-set-arbitrary-scope",
	})
	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Scope != ScopeDownloadUpload {
		t.Fatalf("Scope should have been forced to %q, got %q", ScopeDownloadUpload, claims.Scope)
	}
}

func TestTokenSigner_VerifyRejectsExpired(t *testing.T) {
	// TTL negative -> already expired at issuance
	signer := &TokenSigner{Secret: []byte("s"), TTL: -time.Minute}
	tok, _ := signer.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r"})
	_, err := signer.Verify(tok)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsWrongSecret(t *testing.T) {
	minter := &TokenSigner{Secret: []byte("alpha"), TTL: time.Hour}
	tok, _ := minter.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r"})

	verifier := &TokenSigner{Secret: []byte("beta"), TTL: time.Hour}
	_, err := verifier.Verify(tok)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsTamperedToken(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("s"), TTL: time.Hour}
	tok, _ := signer.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r"})
	tampered := tok[:len(tok)-3] + "AAA" // flip last 3 chars of signature

	_, err := signer.Verify(tampered)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on tampered token, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsAlgNone(t *testing.T) {
	// Defensive: a hand-crafted token with alg=none must be rejected even
	// if the signature field is empty.
	// alg=none base64: eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0
	// claims: {"team_id":"t","upload_id":"u","deploy_run_id":"r","scope":"download-upload","iss":"0ops","aud":["gha-build"],"sub":"upload:u","exp":9999999999}
	header := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0"
	payload := "eyJ0ZWFtX2lkIjoidCIsInVwbG9hZF9pZCI6InUiLCJkZXBsb3lfcnVuX2lkIjoiciIsInNjb3BlIjoiZG93bmxvYWQtdXBsb2FkIiwiaXNzIjoiMG9wcyIsImF1ZCI6WyJnaGEtYnVpbGQiXSwic3ViIjoidXBsb2FkOnUiLCJleHAiOjk5OTk5OTk5OTl9"
	noneToken := header + "." + payload + "."

	signer := &TokenSigner{Secret: []byte("s"), TTL: time.Hour}
	_, err := signer.Verify(noneToken)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid for alg=none, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsWrongAudience(t *testing.T) {
	// Forge a token signed with the same secret but wrong aud.
	// Use jwt.NewWithClaims directly to bypass the Sign() helper.
	s := []byte("s")
	claims := TokenClaims{
		TeamID: "t", UploadID: "u", DeployRunID: "r",
		Scope: ScopeDownloadUpload,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuerOps,
			Audience:  jwt.ClaimStrings{"some-other-audience"},
			Subject:   subjectPrefix + "u",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}

	signer := &TokenSigner{Secret: s, TTL: time.Hour}
	_, err = signer.Verify(raw)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on wrong aud, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsWrongIssuer(t *testing.T) {
	s := []byte("s")
	claims := TokenClaims{
		TeamID: "t", UploadID: "u", DeployRunID: "r",
		Scope: ScopeDownloadUpload,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "evil-issuer",
			Audience:  jwt.ClaimStrings{audienceGHA},
			Subject:   subjectPrefix + "u",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s)

	signer := &TokenSigner{Secret: s, TTL: time.Hour}
	_, err := signer.Verify(raw)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on wrong iss, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsWrongScopeClaim(t *testing.T) {
	// Forge a token with same iss/aud/sub but with scope != download-upload.
	s := []byte("s")
	claims := TokenClaims{
		TeamID: "t", UploadID: "u", DeployRunID: "r",
		Scope: "different-scope",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuerOps,
			Audience:  jwt.ClaimStrings{audienceGHA},
			Subject:   subjectPrefix + "u",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s)

	signer := &TokenSigner{Secret: s, TTL: time.Hour}
	_, err := signer.Verify(raw)
	if !errors.Is(err, ErrTokenScopeMismatch) {
		t.Fatalf("expected ErrTokenScopeMismatch on wrong scope, got %v", err)
	}
}

func TestTokenSigner_VerifyRejectsRS256Token(t *testing.T) {
	// Defensive: a token signed with RS256 (asymmetric) must be rejected
	// even if the key is somehow valid — we ONLY accept HS256.
	// Use a fresh tiny key via the existing jwt library's SigningMethodNone helper
	// is hard, so just attempt verify of any string with RS256 in header.
	// Easier: create a token where header alg="RS256" by hand-encoding.
	rs256Header := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9"
	payload := "eyJzdWIiOiJ0ZXN0In0"
	fakeSig := "AAAA"
	fakeToken := rs256Header + "." + payload + "." + fakeSig

	signer := &TokenSigner{Secret: []byte("s"), TTL: time.Hour}
	_, err := signer.Verify(fakeToken)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid for RS256-claimed token, got %v", err)
	}
}

func TestTokenSigner_SignFailsOnEmptySecret(t *testing.T) {
	signer := &TokenSigner{Secret: nil, TTL: time.Hour}
	_, err := signer.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r"})
	if err == nil {
		t.Fatalf("expected error signing with empty secret")
	}
}

func TestTokenSigner_VerifyFailsOnEmptySecret(t *testing.T) {
	// First mint a token with a real secret so we have something parseable.
	real := &TokenSigner{Secret: []byte("real"), TTL: time.Hour}
	tok, err := real.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Then verify with an empty secret — must fail closed.
	empty := &TokenSigner{Secret: nil, TTL: time.Hour}
	_, err = empty.Verify(tok)
	if err == nil {
		t.Fatalf("Verify with empty secret must fail closed")
	}
}

func TestTokenSigner_VerifyRejectsSubjectUploadIDMismatch(t *testing.T) {
	s := []byte("s")
	claims := TokenClaims{
		TeamID: "t", UploadID: "actual-upload-id", DeployRunID: "r",
		Scope: ScopeDownloadUpload,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuerOps,
			Audience:  jwt.ClaimStrings{audienceGHA},
			Subject:   subjectPrefix + "different-upload-id", // mismatch
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s)

	signer := &TokenSigner{Secret: s, TTL: time.Hour}
	_, err := signer.Verify(raw)
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on Subject/UploadID mismatch, got %v", err)
	}
}
