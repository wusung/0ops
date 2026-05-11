package githuboauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDeviceAuthorizationAndUserFetch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			if r.Method != http.MethodPost {
				t.Fatalf("device code method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if got := r.Form.Get("client_id"); got != "client-123" {
				t.Fatalf("client_id = %q", got)
			}
			if got := r.Form.Get("scope"); got != "user:email" {
				t.Fatalf("scope = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "device-abc",
				"user_code":                 "ABCD-EFGH",
				"verification_uri":          "https://github.com/login/device",
				"verification_uri_complete": "https://github.com/login/device?user_code=ABCD-EFGH",
				"expires_in":                600,
				"interval":                  5,
			})
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				t.Fatalf("access token method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if got := r.Form.Get("device_code"); got != "device-abc" {
				t.Fatalf("device_code = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "gh-access",
				"token_type":   "bearer",
				"scope":        "user:email",
			})
		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer gh-access" {
				t.Fatalf("Authorization = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"login": "owner",
				"name":  "Owner",
				"email": "owner@example.com",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, srv.URL, "client-123", "client-secret", srv.Client())

	challenge, err := client.StartDeviceAuthorization(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceAuthorization() error = %v", err)
	}
	if challenge.DeviceCode != "device-abc" || challenge.UserCode != "ABCD-EFGH" {
		t.Fatalf("challenge = %#v", challenge)
	}

	token, err := client.ExchangeDeviceCode(context.Background(), challenge.DeviceCode)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode() error = %v", err)
	}
	if token.AccessToken != "gh-access" {
		t.Fatalf("AccessToken = %q", token.AccessToken)
	}

	user, err := client.FetchUser(context.Background(), token.AccessToken)
	if err != nil {
		t.Fatalf("FetchUser() error = %v", err)
	}
	if user.Login != "owner" || user.Email != "owner@example.com" {
		t.Fatalf("user = %#v", user)
	}
}

func TestClientDeviceCodePendingErrors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             "authorization_pending",
				"error_description": "pending",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, srv.URL, "client-123", "client-secret", srv.Client())
	_, err := client.ExchangeDeviceCode(context.Background(), "device-abc")
	if !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("ExchangeDeviceCode() error = %v, want ErrAuthorizationPending", err)
	}
}
