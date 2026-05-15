package lint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Verbatim clauses required by the MCP description contract. These literals
// are the only allowed source; do not paraphrase. See spec § 4 hard rule #8.
const (
	AlwaysClause   = "ALWAYS call this BEFORE"
	NeverClause    = "NEVER call this tool without"
	TeamSlugProp   = "team_slug"
	previewSuffix  = "_preview"
	planLocatorMsg = "Refer to docs/0ops-plan.md \"MCP server / Tool description 強制約定\" section for the verbatim templates."
)

// Rule identifiers used in violation messages and tests.
const (
	RuleR1 = "R1-preview-always-before"
	RuleR2 = "R2-write-never-without-preview"
	RuleR3 = "R3-write-team-slug-required"
)

// writeActions is the canonical v1 write/delete action set per
// docs/features/mcp-tool-description-lint/spec.md § 4.3. New write actions
// must be added here together with the matching tool files, registry entry,
// spec table row, and lint fixtures (spec § 12 hard rule #6).
var writeActions = []string{
	"create_app",
	"update_app",
	"delete_app",
	"redeploy",
	"add_domain",
	"remove_domain",
	"invite_member",
	"remove_member",
	"install_github_app",
	"uninstall_github_app",
}

// WriteActions returns a copy of the canonical v1 write/delete action set.
func WriteActions() []string {
	return append([]string(nil), writeActions...)
}

// Tool is the lint-relevant projection of an MCP tool registration.
type Tool struct {
	Name        string
	Description string
	InputSchema *jsonschema.Schema
}

// Violation represents a single lint failure with a stable rule identifier
// suitable for log filtering and tests.
type Violation struct {
	RuleID  string
	Tool    string
	Message string
}

// Error implements the error interface in the format described by the spec
// § 4.1/4.2 examples.
func (v *Violation) Error() string {
	return fmt.Sprintf("[mcp-lint] %s: %s", v.RuleID, v.Message)
}

// ApplyAll evaluates every rule against every tool and returns all violations
// in deterministic order. An empty slice means startup may proceed.
func ApplyAll(tools []Tool) []error {
	var out []error
	for _, t := range tools {
		out = append(out, checkR1(t)...)
		out = append(out, checkR2(t)...)
		out = append(out, checkR3(t)...)
	}
	return out
}

func checkR1(t Tool) []error {
	if !strings.HasSuffix(t.Name, previewSuffix) {
		return nil
	}
	if strings.Contains(t.Description, AlwaysClause) {
		return nil
	}
	return []error{&Violation{
		RuleID: RuleR1,
		Tool:   t.Name,
		Message: fmt.Sprintf(
			"tool %q description must contain the verbatim string %q. Found:\n\n  %s\n\nFix: %s",
			t.Name, AlwaysClause, truncate(t.Description, 200), planLocatorMsg,
		),
	}}
}

func checkR2(t Tool) []error {
	if strings.HasSuffix(t.Name, previewSuffix) {
		return nil
	}
	if !slices.Contains(writeActions, t.Name) {
		return nil
	}
	if strings.Contains(t.Description, NeverClause) {
		return nil
	}
	return []error{&Violation{
		RuleID: RuleR2,
		Tool:   t.Name,
		Message: fmt.Sprintf(
			"tool %q is a write action and must contain the verbatim string %q. Found:\n\n  %s\n\nFix: %s",
			t.Name, NeverClause, truncate(t.Description, 200), planLocatorMsg,
		),
	}}
}

func checkR3(t Tool) []error {
	action := strings.TrimSuffix(t.Name, previewSuffix)
	if !slices.Contains(writeActions, action) {
		return nil
	}
	if hasRequiredTeamSlug(t.InputSchema) {
		return nil
	}
	return []error{&Violation{
		RuleID: RuleR3,
		Tool:   t.Name,
		Message: fmt.Sprintf(
			"tool %q must require %q in input schema. Found schema lacks %q in `required` array.",
			t.Name, TeamSlugProp, TeamSlugProp,
		),
	}}
}

func hasRequiredTeamSlug(s *jsonschema.Schema) bool {
	if s == nil {
		return false
	}
	if _, ok := s.Properties[TeamSlugProp]; !ok {
		return false
	}
	return slices.Contains(s.Required, TeamSlugProp)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
