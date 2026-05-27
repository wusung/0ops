package domainverify

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateVerificationToken(t *testing.T) {
	t.Parallel()
	tok, err := GenerateVerificationToken()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(tok) != 64 {
		t.Fatalf("got len=%d, want 64 hex chars", len(tok))
	}
	if strings.ToLower(tok) != tok {
		t.Fatalf("token must be lowercase hex: %q", tok)
	}
	raw, err := hex.DecodeString(tok)
	if err != nil {
		t.Fatalf("not valid hex: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded len=%d, want 32 bytes", len(raw))
	}
}

func TestGenerateVerificationTokenUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		tok, err := GenerateVerificationToken()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token observed at i=%d: %s", i, tok)
		}
		seen[tok] = struct{}{}
	}
}
