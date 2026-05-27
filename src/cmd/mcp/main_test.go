package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/winshare/zeroops/internal/mcp/lint"
)

func TestReportLintViolationsReturnsZeroWhenClean(t *testing.T) {
	var buf bytes.Buffer
	if got := reportLintViolations(nil, &buf); got != 0 {
		t.Fatalf("exit code on clean lint: got %d want 0", got)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no stderr output on clean lint, got %q", buf.String())
	}
}

func TestReportLintViolationsExitsTwoOnAnyViolation(t *testing.T) {
	violations := []error{
		&lint.Violation{
			RuleID:  lint.RuleR1,
			Tool:    "create_app_preview",
			Message: `tool "create_app_preview" description must contain the verbatim string "ALWAYS call this BEFORE"`,
		},
	}
	var buf bytes.Buffer
	got := reportLintViolations(violations, &buf)
	if got != exitCodeLintFailed {
		t.Fatalf("exit code on violation: got %d want %d", got, exitCodeLintFailed)
	}
	out := buf.String()
	if !strings.Contains(out, "R1-preview-always-before") {
		t.Errorf("stderr should mention rule id, got %q", out)
	}
	if !strings.Contains(out, "Aborting startup") {
		t.Errorf("stderr should announce abort, got %q", out)
	}
	if !strings.Contains(out, "1 violation(s)") {
		t.Errorf("stderr should summarize violation count, got %q", out)
	}
}

// TestStartupLintWouldRejectBadDescription proves that the lint plumbing
// rejects an intentionally malformed create_app_preview description with
// exit code 2 - the spec § 9 negative-path acceptance check.
func TestStartupLintWouldRejectBadDescription(t *testing.T) {
	bad := []lint.Tool{
		{
			Name:        "create_app_preview",
			Description: "Preview side effects of create_app.",
		},
		{
			Name:        "create_app",
			Description: "Confirm create_app using preview_id.",
		},
	}
	errs := lint.ApplyAll(bad)
	if len(errs) == 0 {
		t.Fatal("lint must flag malformed descriptions")
	}

	var buf bytes.Buffer
	if got := reportLintViolations(errs, &buf); got != exitCodeLintFailed {
		t.Fatalf("startup must exit %d on lint failure: got %d", exitCodeLintFailed, got)
	}
}
