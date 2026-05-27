package localbuild

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallbackClientSignsAndPosts(t *testing.T) {
	secret := "dev-callback-secret-change-me"
	var gotBody []byte
	var gotSig string
	var gotTS string
	var gotURL string
	var gotTrace string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-0ops-Signature")
		gotTS = r.Header.Get("X-0ops-Timestamp")
		gotURL = r.URL.Path
		gotTrace = r.Header.Get("X-Trace-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCallbackClient(srv.URL, secret, http.DefaultClient)
	if err := c.Send(context.Background(), "dr_test", CallbackEvent{Status: "building", TraceID: "trace-local-1"}); err != nil {
		t.Fatal(err)
	}

	if gotURL != "/internal/deploy-runs/dr_test/callback" {
		t.Errorf("url=%q", gotURL)
	}
	if gotTS == "" {
		t.Errorf("X-0ops-Timestamp missing")
	}
	if gotTrace != "trace-local-1" {
		t.Errorf("X-Trace-ID = %q, want trace-local-1", gotTrace)
	}

	var ev CallbackEvent
	if err := json.Unmarshal(gotBody, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Status != "building" {
		t.Errorf("status=%q", ev.Status)
	}
	if ev.RunID != "dr_test" {
		t.Errorf("run_id=%q want dr_test", ev.RunID)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(gotTS))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(gotBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != expected {
		t.Errorf("sig mismatch:\n got %s\nwant %s", gotSig, expected)
	}
}

func TestCallbackClientReturnsErrorOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewCallbackClient(srv.URL, "k", http.DefaultClient)
	err := c.Send(context.Background(), "dr_x", CallbackEvent{Status: "live"})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}
