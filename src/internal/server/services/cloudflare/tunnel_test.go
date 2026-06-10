package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetTunnelConnectorsReadyCountsActiveConnections(t *testing.T) {
	var seen int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&seen, 1)
		if !strings.HasPrefix(r.URL.Path, "/accounts/account-1/cfd_tunnel/tunnel-1/connections") {
			t.Fatalf("path = %q, want /accounts/account-1/cfd_tunnel/tunnel-1/connections", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(tunnelConnectorsEnvelope{
			Success: true,
			Result: []struct {
				ID string `json:"id"`
			}{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}},
		})
	}))
	defer srv.Close()

	client, err := newClient(&Config{
		TunnelID:  "tunnel-1",
		APIToken:  "token",
		AccountID: "account-1",
		ZoneID:    "zone-1",
	}, srv.URL, srv.Client(), func(time.Duration) {})
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	count, err := client.GetTunnelConnectorsReady(context.Background())
	if err != nil {
		t.Fatalf("GetTunnelConnectorsReady() error = %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if got := atomic.LoadInt32(&seen); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestRequestRecordsCallDuration(t *testing.T) {
	durations := make(map[string]time.Duration)
	BindCallDurationMetric(func(op string, latency time.Duration) {
		durations[op] = latency
	})
	defer BindCallDurationMetric(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(apiEnvelope{
			Success: true,
			Result: []dnsRecord{{
				ID: "dns-1", Name: "*.jesontech.com", Type: "CNAME", Content: "abcd.cfargotunnel.com",
			}},
		})
	}))
	defer srv.Close()

	client, err := newClient(&Config{
		TunnelID:  "tunnel-1",
		APIToken:  "token",
		AccountID: "account-1",
		ZoneID:    "zone-1",
	}, srv.URL, srv.Client(), func(time.Duration) {})
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	if _, err := client.RouteAppToDomain(context.Background(), "team-1", "team-slug", "nextdemo"); err != nil {
		t.Fatalf("RouteAppToDomain() error = %v", err)
	}

	if _, ok := durations["dns_list"]; !ok {
		t.Fatalf("dns_list duration not recorded; got %#v", durations)
	}
}
