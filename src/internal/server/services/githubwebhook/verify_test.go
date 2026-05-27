package githubwebhook

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadBoundedBodyAcceptsSmallPayloads(t *testing.T) {
	body := strings.Repeat("a", 1024)
	req := httptest.NewRequest("POST", "/v1/webhooks/github", bytes.NewBufferString(body))

	got, err := ReadBoundedBody(req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(got) != body {
		t.Fatalf("body mismatch (len %d vs %d)", len(got), len(body))
	}

	// req.Body must still be readable for downstream verifier.
	reread, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("re-read err = %v", err)
	}
	if string(reread) != body {
		t.Fatalf("re-read mismatch")
	}
}

func TestReadBoundedBodyRejectsOverflow(t *testing.T) {
	body := bytes.Repeat([]byte("a"), MaxPayloadBytes+1)
	req := httptest.NewRequest("POST", "/v1/webhooks/github", bytes.NewReader(body))

	_, err := ReadBoundedBody(req)
	if err == nil {
		t.Fatalf("expected PayloadTooLargeError")
	}
	var tooLarge PayloadTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want PayloadTooLargeError", err)
	}
}
