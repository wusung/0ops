package token

import (
	"strings"
	"testing"
)

func TestBearerTokenRoundTrip(t *testing.T) {
	got, err := NewBearerToken("pat", "token-123")
	if err != nil {
		t.Fatalf("NewBearerToken() error = %v", err)
	}

	parsed, err := ParseBearerToken(got)
	if err != nil {
		t.Fatalf("ParseBearerToken() error = %v", err)
	}
	if parsed.Kind != "pat" || parsed.ID != "token-123" || parsed.Secret == "" {
		t.Fatalf("unexpected parsed token: %#v", parsed)
	}
	if !strings.HasPrefix(got, "op_pat_token-123.") {
		t.Fatalf("unexpected token format: %s", got)
	}
}

func TestHashBearerTokenCompare(t *testing.T) {
	hash1, err := HashBearerToken("secret-a")
	if err != nil {
		t.Fatalf("HashBearerToken() error = %v", err)
	}
	hash2, err := HashBearerToken("secret-a")
	if err != nil {
		t.Fatalf("HashBearerToken() error = %v", err)
	}
	if hash1 == hash2 {
		t.Fatal("expected unique salts to produce different hashes")
	}

	ok, err := CompareBearerToken("secret-a", hash1)
	if err != nil {
		t.Fatalf("CompareBearerToken() error = %v", err)
	}
	if !ok {
		t.Fatal("expected matching secret to verify")
	}

	ok, err = CompareBearerToken("secret-b", hash1)
	if err != nil {
		t.Fatalf("CompareBearerToken() error = %v", err)
	}
	if ok {
		t.Fatal("expected different secret to fail verification")
	}
}

func TestParseBearerTokenRejectsInvalidFormat(t *testing.T) {
	if _, err := ParseBearerToken("bad-token"); err == nil {
		t.Fatal("expected invalid token to fail")
	}
}
