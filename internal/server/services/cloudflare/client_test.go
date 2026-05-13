package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouteAppToDomainValidatesWildcardRoute(t *testing.T) {
	var seen int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&seen, 1)
		if got := r.URL.Path; got != "/zones/zone-1/dns_records" {
			t.Fatalf("path = %q, want /zones/zone-1/dns_records", got)
		}
		values, _ := url.ParseQuery(r.URL.RawQuery)
		if got := values.Get("type"); got != "CNAME" {
			t.Fatalf("type = %q, want CNAME", got)
		}
		if got := values.Get("name"); got != "*.winshare.tw" {
			t.Fatalf("name = %q, want *.winshare.tw", got)
		}

		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Success: true,
			Result: []dnsRecord{
				{
					ID:      "dns-1",
					Name:    "*.winshare.tw",
					Type:    "CNAME",
					Content: "abcd.cfargotunnel.com",
					Proxied: true,
					TTL:     1,
				},
			},
		})
	}))
	defer srv.Close()

	client, err := newClient(&Config{
		TunnelID:  "abcd",
		APIToken:  "token",
		AccountID: "account-1",
		ZoneID:    "zone-1",
	}, srv.URL, srv.Client(), func(time.Duration) {})
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	domain, err := client.RouteAppToDomain(context.Background(), "team-1", "team-slug", "nextdemo")
	if err != nil {
		t.Fatalf("RouteAppToDomain() error = %v", err)
	}
	if domain != "nextdemo.winshare.tw" {
		t.Fatalf("domain = %q, want nextdemo.winshare.tw", domain)
	}
	if atomic.LoadInt32(&seen) != 1 {
		t.Fatalf("request count = %d, want 1", seen)
	}
}

func TestRouteAppToDomainReturnsRateLimitedAfterRetries(t *testing.T) {
	var seen int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&seen, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client, err := newClient(&Config{
		TunnelID:  "abcd",
		APIToken:  "token",
		AccountID: "account-1",
		ZoneID:    "zone-1",
	}, srv.URL, srv.Client(), func(time.Duration) {})
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	_, err = client.RouteAppToDomain(context.Background(), "team-1", "team-slug", "nextdemo")
	if err == nil {
		t.Fatal("RouteAppToDomain() error = nil, want rate limit")
	}
	if err != ErrRateLimited {
		t.Fatalf("error = %v, want ErrRateLimited", err)
	}
	if got := atomic.LoadInt32(&seen); got != 5 {
		t.Fatalf("request count = %d, want 5", got)
	}
}
