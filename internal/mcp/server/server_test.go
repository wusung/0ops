package server

import (
	"log/slog"
	"testing"

	"github.com/winshare/zeroops/internal/shared"
)

func TestImplementationUsesSharedVersion(t *testing.T) {
	impl := Implementation()

	if impl.Name != "0ops-mcp" {
		t.Fatalf("Name = %q, want %q", impl.Name, "0ops-mcp")
	}
	if impl.Version != shared.Version {
		t.Fatalf("Version = %q, want %q", impl.Version, shared.Version)
	}
}

func TestNewReturnsServer(t *testing.T) {
	logger := slog.Default()
	if srv := New(logger); srv == nil {
		t.Fatal("New() returned nil")
	}
}
