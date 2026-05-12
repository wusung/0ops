package cloudflare

import (
	"context"
	"testing"
)

func TestRouteAppToDomainUsesWinshareSubdomain(t *testing.T) {
	client, err := NewClient(&Config{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	got, err := client.RouteAppToDomain(context.Background(), "team-1", "acme", "nextdemo")
	if err != nil {
		t.Fatalf("RouteAppToDomain() error = %v", err)
	}
	if got != "nextdemo.winshare.tw" {
		t.Fatalf("RouteAppToDomain() = %q, want nextdemo.winshare.tw", got)
	}
}

func TestRouteAppToDomainRejectsEmptySlug(t *testing.T) {
	client, err := NewClient(&Config{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.RouteAppToDomain(context.Background(), "team-1", "acme", ""); err == nil {
		t.Fatalf("RouteAppToDomain() error = nil, want non-nil for empty app slug")
	}
}
