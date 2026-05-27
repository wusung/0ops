package lint

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func objectSchema(props map[string]*jsonschema.Schema, required ...string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:       "object",
		Properties: props,
		Required:   append([]string(nil), required...),
	}
}

func teamSlugSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{
		"team_slug": {Type: "string"},
	}, "team_slug")
}

func TestWriteActionsMatchesSpec(t *testing.T) {
	want := []string{
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
	got := WriteActions()
	if len(got) != len(want) {
		t.Fatalf("WriteActions length: got %d want %d (%v)", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("WriteActions[%d]: got %q want %q", i, got[i], name)
		}
	}
}

func TestRule1PreviewMustContainAlwaysClause(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app_preview",
			Description: "Preview side effects of create_app.",
			InputSchema: teamSlugSchema(),
		},
	}
	errs := ApplyAll(tools)
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "R1-preview-always-before") &&
			strings.Contains(err.Error(), "create_app_preview") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected R1 violation for create_app_preview, got %v", errs)
	}
}

func TestRule1PreviewPassesWhenAlwaysClausePresent(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app_preview",
			Description: "ALWAYS call this BEFORE create_app to obtain a preview_id.",
			InputSchema: teamSlugSchema(),
		},
	}
	for _, err := range ApplyAll(tools) {
		if strings.Contains(err.Error(), "R1-preview-always-before") {
			t.Fatalf("R1 should not fire when ALWAYS clause present: %v", err)
		}
	}
}

func TestRule2WriteActionMustContainNeverClause(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app",
			Description: "Confirm create_app using preview_id.",
			InputSchema: teamSlugSchema(),
		},
	}
	errs := ApplyAll(tools)
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "R2-write-never-without-preview") &&
			strings.Contains(err.Error(), "create_app") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected R2 violation for create_app, got %v", errs)
	}
}

func TestRule2WritePassesWhenNeverClausePresent(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app",
			Description: "Confirm create_app using preview_id. NEVER call this tool without a fresh preview_id.",
			InputSchema: teamSlugSchema(),
		},
	}
	for _, err := range ApplyAll(tools) {
		if strings.Contains(err.Error(), "R2-write-never-without-preview") {
			t.Fatalf("R2 should not fire when NEVER clause present: %v", err)
		}
	}
}

func TestRule3WriteToolRequiresTeamSlug(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app",
			Description: "Confirm create_app using preview_id. NEVER call this tool without a fresh preview_id.",
			InputSchema: objectSchema(map[string]*jsonschema.Schema{
				"preview_id": {Type: "string"},
			}, "preview_id"),
		},
	}
	errs := ApplyAll(tools)
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "R3-write-team-slug-required") &&
			strings.Contains(err.Error(), "create_app") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected R3 violation for create_app missing team_slug, got %v", errs)
	}
}

func TestRule3WriteToolWithTeamSlugInPropsButNotRequiredFails(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app",
			Description: "Confirm create_app using preview_id. NEVER call this tool without a fresh preview_id.",
			InputSchema: objectSchema(map[string]*jsonschema.Schema{
				"team_slug":  {Type: "string"},
				"preview_id": {Type: "string"},
			}, "preview_id"),
		},
	}
	errs := ApplyAll(tools)
	for _, err := range errs {
		if strings.Contains(err.Error(), "R3-write-team-slug-required") {
			return
		}
	}
	t.Fatalf("expected R3 violation when team_slug present but not in required, got %v", errs)
}

func TestRule3PreviewToolAlsoChecked(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app_preview",
			Description: "ALWAYS call this BEFORE create_app to obtain a preview_id.",
			InputSchema: objectSchema(map[string]*jsonschema.Schema{
				"slug": {Type: "string"},
			}, "slug"),
		},
	}
	errs := ApplyAll(tools)
	for _, err := range errs {
		if strings.Contains(err.Error(), "R3-write-team-slug-required") {
			return
		}
	}
	t.Fatalf("expected R3 violation for create_app_preview missing team_slug, got %v", errs)
}

func TestReadToolsAreNotLinted(t *testing.T) {
	tools := []Tool{
		{
			Name:        "list_apps",
			Description: "List apps in a team.",
			InputSchema: teamSlugSchema(),
		},
		{
			Name:        "tail_logs",
			Description: "Tail latest deploy logs for an app.",
			InputSchema: teamSlugSchema(),
		},
	}
	if errs := ApplyAll(tools); len(errs) != 0 {
		t.Fatalf("expected zero violations for read tools, got %v", errs)
	}
}

func TestApplyAllReportsEveryViolation(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app_preview",
			Description: "Preview side effects of create_app.",
			InputSchema: objectSchema(map[string]*jsonschema.Schema{}, ""),
		},
		{
			Name:        "create_app",
			Description: "Confirm create_app using preview_id.",
			InputSchema: objectSchema(map[string]*jsonschema.Schema{}, ""),
		},
	}
	errs := ApplyAll(tools)
	if len(errs) < 4 {
		t.Fatalf("expected at least 4 violations (R1+R3 on preview, R2+R3 on confirm), got %d: %v", len(errs), errs)
	}
}

func TestApplyAllReturnsTypedViolations(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app_preview",
			Description: "Preview side effects of create_app.",
			InputSchema: teamSlugSchema(),
		},
	}
	errs := ApplyAll(tools)
	if len(errs) == 0 {
		t.Fatal("expected at least one violation")
	}
	var v *Violation
	if !errors.As(errs[0], &v) {
		t.Fatalf("expected *Violation, got %T", errs[0])
	}
	if v.RuleID == "" || v.Tool == "" {
		t.Fatalf("Violation fields empty: %+v", v)
	}
}

func TestRule2DoesNotFireOnPreviewSuffix(t *testing.T) {
	tools := []Tool{
		{
			Name:        "create_app_preview",
			Description: "ALWAYS call this BEFORE create_app to obtain a preview_id.",
			InputSchema: teamSlugSchema(),
		},
	}
	for _, err := range ApplyAll(tools) {
		if strings.Contains(err.Error(), "R2-write-never-without-preview") {
			t.Fatalf("R2 should not fire on _preview tool: %v", err)
		}
	}
}
