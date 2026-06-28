package security

import "testing"

func TestRiskLevelCatalog(t *testing.T) {
	cases := []struct {
		name   string
		action string
		args   map[string]any
		want   Level
	}{
		{"delete_app is critical", "delete_app", map[string]any{"confirm": "myapp"}, RiskCritical},
		{"token_revoke is critical", "token_revoke", nil, RiskCritical},
		{"custom_domain_unbind is high", "custom_domain_unbind", nil, RiskHigh},
		{"uninstall_github_app is high", "uninstall_github_app", nil, RiskHigh},
		{"create_app is normal", "create_app", map[string]any{"slug": "x"}, RiskNormal},
		{"redeploy is normal", "redeploy", nil, RiskNormal},
		{"unknown action is normal", "frobnicate", nil, RiskNormal},
		{"plan_change downgrade is high", "plan_change", map[string]any{"from": "pro", "to": "starter"}, RiskHigh},
		{"plan_change upgrade is normal", "plan_change", map[string]any{"from": "starter", "to": "pro"}, RiskNormal},
		{"plan_change same tier is normal", "plan_change", map[string]any{"from": "pro", "to": "pro"}, RiskNormal},
		{"remove_member owner is high", "remove_member", map[string]any{"role": "owner"}, RiskHigh},
		{"remove_member admin is high", "remove_member", map[string]any{"role": "admin"}, RiskHigh},
		{"remove_member member is normal", "remove_member", map[string]any{"role": "member"}, RiskNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RiskLevel(tc.action, tc.args); got != tc.want {
				t.Fatalf("RiskLevel(%q, %v) = %q, want %q", tc.action, tc.args, got, tc.want)
			}
		})
	}
}

func TestRequiredPhrase(t *testing.T) {
	t.Run("delete_app yields DELETE <slug>", func(t *testing.T) {
		got := RequiredPhrase("delete_app", map[string]any{"confirm": "billing-api"})
		if got != "DELETE billing-api" {
			t.Fatalf("RequiredPhrase delete_app = %q, want %q", got, "DELETE billing-api")
		}
	})
	t.Run("normal action yields empty phrase", func(t *testing.T) {
		if got := RequiredPhrase("create_app", map[string]any{"slug": "x"}); got != "" {
			t.Fatalf("RequiredPhrase create_app = %q, want empty", got)
		}
	})
	t.Run("high-risk action without resolvable subject yields empty", func(t *testing.T) {
		// token_revoke has no preview/confirm wiring in v1: no subject in args.
		if got := RequiredPhrase("token_revoke", nil); got != "" {
			t.Fatalf("RequiredPhrase token_revoke = %q, want empty", got)
		}
	})
}

func TestIsHigherRisk(t *testing.T) {
	if !RiskCritical.AtLeast(RiskHigh) {
		t.Fatal("critical should be >= high")
	}
	if RiskNormal.AtLeast(RiskHigh) {
		t.Fatal("normal should not be >= high")
	}
	if !RiskHigh.AtLeast(RiskHigh) {
		t.Fatal("high should be >= high")
	}
}
