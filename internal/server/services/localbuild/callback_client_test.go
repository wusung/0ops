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
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Ops-Signature")
		gotURL = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCallbackClient(srv.URL, secret, http.DefaultClient)
	if err := c.Send(context.Background(), "dr_test", CallbackEvent{Status: "building"}); err != nil {
		t.Fatal(err)
	}

	if gotURL != "/internal/deploy-runs/dr_test/callback" {
		t.Errorf("url=%q", gotURL)
	}

	var ev CallbackEvent
	if err := json.Unmarshal(gotBody, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Status != "building" {
		t.Errorf("status=%q", ev.Status)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	expected := "hmac-sha256=" + hex.EncodeToString(mac.Sum(nil))
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
