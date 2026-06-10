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

	opsruntime "github.com/winshare/zeroops/internal/shared/runtime"
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
		if got, want := values.Get("name"), "*."+opsruntime.DomainBase(); got != want {
			t.Fatalf("name = %q, want %s", got, want)
		}

		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Success: true,
			Result: []dnsRecord{
				{
					ID:      "dns-1",
					Name:    "*." + opsruntime.DomainBase(),
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
	if want := opsruntime.AppHostname("nextdemo"); domain != want {
		t.Fatalf("domain = %q, want %s", domain, want)
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

func TestClientRecordsOperationMetrics(t *testing.T) {
	calls := make([]string, 0, 3)
	BindMetrics(func(op, outcome string) {
		calls = append(calls, op+":"+outcome)
	})
	defer BindMetrics(nil)

	client, err := NewClient(&Config{DisableTunnelIsolation: true})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := client.RouteAppToDomain(context.Background(), "team-1", "acme", "nextdemo"); err != nil {
		t.Fatalf("RouteAppToDomain() error = %v", err)
	}
	if err := client.CreateTunnelRoute(context.Background(), "team-1", "nextdemo", "http://backend:8080"); err != nil {
		t.Fatalf("CreateTunnelRoute() error = %v", err)
	}
	if err := client.DeleteTunnelRoute(context.Background(), "nextdemo.acme.jesontech.com"); err != nil {
		t.Fatalf("DeleteTunnelRoute() error = %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("metric calls = %d, want 3 (%v)", len(calls), calls)
	}
	if calls[0] != "dns_create:success" {
		t.Fatalf("call[0] = %q, want dns_create:success", calls[0])
	}
	if calls[1] != "tunnel_route_create:success" {
		t.Fatalf("call[1] = %q, want tunnel_route_create:success", calls[1])
	}
	if calls[2] != "tunnel_route_delete:success" {
		t.Fatalf("call[2] = %q, want tunnel_route_delete:success", calls[2])
	}
}
