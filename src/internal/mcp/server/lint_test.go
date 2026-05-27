package server

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/mcp/lint"
)

// TestRegisteredToolsPassStartupLint guarantees the production MCP server
// registration is compliant with spec § 4 rules R1/R2/R3. If a new tool is
// added without the required clauses, this test fails before the binary
// would have aborted on startup (exit 2).
func TestRegisteredToolsPassStartupLint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, reg := NewWithRegistry(logger)

	tools := reg.Tools()
	if len(tools) == 0 {
		t.Fatal("registry captured zero tools; registration wiring broken")
	}

	if errs := lint.ApplyAll(tools); len(errs) > 0 {
		for _, err := range errs {
			t.Errorf("startup lint must pass for production tools: %v", err)
		}
	}
}

// TestCreateAppToolsContainRequiredClauses pins the ALWAYS / NEVER verbatim
// substrings on the create_app pair, the M2.7 acceptance bullet.
func TestCreateAppToolsContainRequiredClauses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, reg := NewWithRegistry(logger)

	descByName := map[string]string{}
	for _, tool := range reg.Tools() {
		descByName[tool.Name] = tool.Description
	}

	preview, ok := descByName["create_app_preview"]
	if !ok {
		t.Fatal("create_app_preview not registered")
	}
	if !strings.Contains(preview, lint.AlwaysClause) {
		t.Errorf("create_app_preview description missing %q: %s", lint.AlwaysClause, preview)
	}

	create, ok := descByName["create_app"]
	if !ok {
		t.Fatal("create_app not registered")
	}
	if !strings.Contains(create, lint.NeverClause) {
		t.Errorf("create_app description missing %q: %s", lint.NeverClause, create)
	}
}
