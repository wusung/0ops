// Package lint enforces the MCP tool description contract defined in
// docs/features/mcp-tool-description-lint/spec.md. Rules are evaluated at
// 0ops-mcp startup; any violation must abort the process before the stdio
// loop begins.
package lint
