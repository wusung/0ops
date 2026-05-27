// Package mcp provides MCP-server-wide helpers shared between the SDK
// registration plumbing and the startup lint contract.
package mcp

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/winshare/zeroops/internal/mcp/lint"
)

// Registry captures the lint-relevant metadata of every tool registered with
// the MCP server. The SDK does not expose a public iterator over registered
// tools, so we record metadata at registration time and feed it to
// lint.ApplyAll at startup (spec § 4.7).
type Registry struct {
	tools []lint.Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Tools returns a copy of the recorded tools, in registration order.
func (r *Registry) Tools() []lint.Tool {
	out := make([]lint.Tool, len(r.tools))
	copy(out, r.tools)
	return out
}

// AddTool registers a tool with the SDK server while capturing its
// lint-relevant metadata in the registry. Use this in place of
// sdkmcp.AddTool so any new tool is automatically subject to the startup
// lint contract.
func AddTool[In, Out any](
	srv *sdkmcp.Server,
	reg *Registry,
	tool *sdkmcp.Tool,
	handler sdkmcp.ToolHandlerFor[In, Out],
) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Errorf("infer input schema for tool %q: %w", tool.Name, err))
	}
	reg.tools = append(reg.tools, lint.Tool{
		Name:        tool.Name,
		Description: tool.Description,
		InputSchema: schema,
	})
	sdkmcp.AddTool(srv, tool, handler)
}
