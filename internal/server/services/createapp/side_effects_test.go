package createapp

import (
	"context"
	"testing"
)

func TestSideEffects(t *testing.T) {
	s := &Service{}
	args := &AppCreateArgs{
		Slug:       "test-app",
		RepoURL:    "https://github.com/owner/repo",
		Ref:        "main",
		Builder:    "docker",
		DomainName: "app.example.com",
	}

	effects, err := s.SideEffects(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(effects) != 5 {
		t.Fatalf("expected 5 effects, got %d", len(effects))
	}

	expectedNames := []string{"db_insert", "k3s_namespace", "gitops_render_push", "ops_token_sign", "gha_dispatch"}
	for i, effect := range effects {
		if effect.Name() != expectedNames[i] {
			t.Errorf("effect %d: expected name %q, got %q", i, expectedNames[i], effect.Name())
		}
	}
}

func TestSideEffectNamesAndDescriptions(t *testing.T) {
	tests := []struct {
		name        string
		effect      SideEffect
		expectedMsg string
	}{
		{
			name:        "db_insert",
			effect:      &DBInsertSideEffect{AppID: "test123"},
			expectedMsg: "db_insert",
		},
		{
			name:        "k3s_namespace",
			effect:      &K3sNamespaceSideEffect{AppID: "test123", Slug: "my-app"},
			expectedMsg: "k3s_namespace",
		},
		{
			name:        "gitops_render_push",
			effect:      &GitOpsRenderPushSideEffect{AppID: "test123", Slug: "my-app", Ref: "main"},
			expectedMsg: "gitops_render_push",
		},
		{
			name:        "ops_token_sign",
			effect:      &OpsTokenSignSideEffect{AppID: "test123"},
			expectedMsg: "ops_token_sign",
		},
		{
			name:        "gha_dispatch",
			effect:      &GHADispatchSideEffect{AppID: "test123", Ref: "main"},
			expectedMsg: "gha_dispatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.effect.Name() != tt.expectedMsg {
				t.Errorf("expected name %q, got %q", tt.expectedMsg, tt.effect.Name())
			}
		})
	}
}
