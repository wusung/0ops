package notify

import "testing"

func TestGenerateSigningKeyLengthAndRoundtrip(t *testing.T) {
	raw, b64, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 32 {
		t.Fatalf("key length = %d, want >= 32", len(raw))
	}
	decoded, err := DecodeSigningKey(b64)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != string(raw) {
		t.Fatal("decoded key != generated key")
	}
}

func TestGenerateSigningKeyIsRandom(t *testing.T) {
	_, a, _ := GenerateSigningKey()
	_, b, _ := GenerateSigningKey()
	if a == b {
		t.Fatal("two generated keys are identical")
	}
}
