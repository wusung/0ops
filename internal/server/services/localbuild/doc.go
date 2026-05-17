// Package localbuild provides a dev-only implementation of
// createapp.Dispatcher that runs paketo `pack build` against a local
// file:// repo and reports state transitions back via the existing callback
// handler. Per ADR-0012; never enabled in production.
package localbuild
