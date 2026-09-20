// Package shared provides process-wide shared values.
package shared

// Version is overridden at build time via -ldflags "-X github.com/wusung/0ops/internal/shared.Version=...".
var Version = "dev"
