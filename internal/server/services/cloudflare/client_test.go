package cloudflare

import (
	"context"
	"testing"
)

func TestClientRecordsOperationMetrics(t *testing.T) {
	calls := make([]string, 0, 4)
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
	if err := client.DeleteTunnelRoute(context.Background(), "nextdemo.acme.winshare.tw"); err != nil {
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
