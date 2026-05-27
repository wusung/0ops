package workflowdispatch

import (
	"testing"
	"time"
)

func TestOpsTokenSignerRoundTrip(t *testing.T) {
	signer := &OpsTokenSigner{secret: []byte("signing-secret"), now: func() time.Time { return time.Now().UTC() }}
	token, err := signer.Issue("run-1", "trace-1", []string{"ghcr:push"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	payload, err := ParseOpsToken(token, []byte("signing-secret"))
	if err != nil {
		t.Fatalf("ParseOpsToken() error = %v", err)
	}
	if payload.RunID != "run-1" || payload.TraceID != "trace-1" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Scopes) != 1 || payload.Scopes[0] != "ghcr:push" {
		t.Fatalf("payload scopes = %#v", payload.Scopes)
	}
}

func TestParseOpsTokenRejectsTamper(t *testing.T) {
	signer := &OpsTokenSigner{secret: []byte("signing-secret"), now: func() time.Time { return time.Now().UTC() }}
	token, err := signer.Issue("run-1", "trace-1", []string{"ghcr:push"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if _, err := ParseOpsToken(token+"x", []byte("signing-secret")); err == nil {
		t.Fatal("expected tamper rejection")
	}
}
