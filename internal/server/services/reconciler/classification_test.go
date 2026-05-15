package reconciler

import "testing"

func TestClassifyWorkflowRunCoversSpecSignals(t *testing.T) {
	cases := []struct {
		name     string
		outcome  WorkflowRunOutcome
		expected Classification
	}{
		{
			name:     "timed_out → build_timeout",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "timed_out"},
			expected: ClassBuildTimeout,
		},
		{
			name:     "docker login failure → registry_auth_failed",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "docker login"},
			expected: ClassRegistryAuthFailed,
		},
		{
			name:     "trivy scan block → image_scan_blocked",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "Trivy scan"},
			expected: ClassImageScanBlocked,
		},
		{
			name:     "buildpack detect failure",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "pack build", StepLog: "no buildpack groups passed detection"},
			expected: ClassBuildpackDetectFailed,
		},
		{
			name:     "registry push failure via pack build --publish",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "pack build", StepLog: "publish failed: 503"},
			expected: ClassRegistryPushFailed,
		},
		{
			name:     "compile error otherwise",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "pack build", StepLog: "compile error"},
			expected: ClassBuildCompileError,
		},
		{
			name:     "git push conflict",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "git push"},
			expected: ClassGitOpsPushConflict,
		},
		{
			name:     "ops_token expired",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepLog: "ops_token expired"},
			expected: ClassBuildSecretExpired,
		},
		{
			name:     "git checkout fail",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "checkout"},
			expected: ClassRepoCheckoutFailed,
		},
		{
			name:     "cancelled → unknown",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "cancelled"},
			expected: ClassUnknown,
		},
		{
			name:     "non-matching failure falls back to unknown",
			outcome:  WorkflowRunOutcome{Status: "completed", Conclusion: "failure", StepName: "do something weird"},
			expected: ClassUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWorkflowRun(tc.outcome)
			if got != tc.expected {
				t.Fatalf("ClassifyWorkflowRun = %s, want %s", got, tc.expected)
			}
		})
	}
}

func TestClassifyArgoCD(t *testing.T) {
	cases := []struct {
		name     string
		health   ArgoCDHealth
		expected Classification
	}{
		{"degraded", ArgoCDHealth{Health: "Degraded", Sync: "Synced"}, ClassHealthCheckFailed},
		{"missing", ArgoCDHealth{Health: "Missing"}, ClassArgoSyncTimeout},
		{"outofsync", ArgoCDHealth{Health: "Progressing", Sync: "OutOfSync"}, ClassArgoSyncTimeout},
		{"fallback", ArgoCDHealth{Health: "Suspended"}, ClassArgoSyncTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyArgoCD(tc.health)
			if got != tc.expected {
				t.Fatalf("ClassifyArgoCD = %s, want %s", got, tc.expected)
			}
		})
	}
}

func TestAllClassificationsAreValid(t *testing.T) {
	for _, c := range AllClassifications {
		if !IsValid(c) {
			t.Fatalf("IsValid(%s) = false", c)
		}
	}
	if IsValid("not_a_real_class") {
		t.Fatalf("IsValid(not_a_real_class) = true")
	}
}

func TestClassificationPtr(t *testing.T) {
	ptr := ClassUnknown.Ptr()
	if ptr == nil || *ptr != "unknown" {
		t.Fatalf("ClassUnknown.Ptr() = %v, want pointer to 'unknown'", ptr)
	}
}
