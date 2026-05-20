package runtime

import (
	"testing"
)

func TestEnvKind(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		wantKind EnvKind
		wantProd bool
	}{
		{"", EnvDevelopment, false},
		{"development", EnvDevelopment, false},
		{"DEVELOPMENT", EnvDevelopment, false},
		{"staging", EnvStaging, false},
		{"production", EnvProduction, true},
		{"PRODUCTION", EnvProduction, true},
		{"garbage", EnvDevelopment, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("OPS_ENV", tc.raw)
			got := CurrentEnv()
			if got != tc.wantKind {
				t.Fatalf("CurrentEnv()=%v want %v", got, tc.wantKind)
			}
			if IsProduction() != tc.wantProd {
				t.Fatalf("IsProduction()=%v want %v", IsProduction(), tc.wantProd)
			}
		})
	}
}

func TestAssertProductionSafePanicsWhenLocalFlagsOn(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic, got none")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafePanicsOnLocalRegistry(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "registry:5000")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic, got none")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafeNoopInDev(t *testing.T) {
	t.Setenv("OPS_ENV", "development")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	t.Setenv("LOCAL_BUILD_ENABLED", "true")
	t.Setenv("LOCAL_REGISTRY", "registry:5000")
	AssertProductionSafe() // must not panic
}

func TestAssertProductionSafePanicsOnMissingIngestRoot(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	// Ensure ADR-0012 checks would NOT trigger
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "")
	// Only OPS_BUILD_TOKEN_SECRET set; INGEST_ROOT missing
	t.Setenv("APP_SOURCE_INGEST_ROOT", "")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "x")
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for missing APP_SOURCE_INGEST_ROOT")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafePanicsOnMissingBuildTokenSecret(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "/var/lib/0ops/uploads")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "")
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for missing OPS_BUILD_TOKEN_SECRET")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafePassesWhenProductionWithUploadEnv(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "/var/lib/0ops/uploads")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "deadbeef")
	AssertProductionSafe() // must NOT panic
}
