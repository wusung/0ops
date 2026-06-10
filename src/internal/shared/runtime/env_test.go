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

func TestDomainBase(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", "jesontech.com"},
		{"   ", "jesontech.com"},
		{"example.org", "example.org"},
		{"  custom.dev  ", "custom.dev"},
	} {
		t.Run("raw="+tc.raw, func(t *testing.T) {
			t.Setenv("OPS_DOMAIN_BASE", tc.raw)
			if got := DomainBase(); got != tc.want {
				t.Fatalf("DomainBase()=%q want %q", got, tc.want)
			}
		})
	}
}

func TestAppHostname(t *testing.T) {
	t.Setenv("OPS_DOMAIN_BASE", "")
	if got := AppHostname("nextdemo"); got != "nextdemo.jesontech.com" {
		t.Fatalf("AppHostname()=%q want nextdemo.jesontech.com", got)
	}
	t.Setenv("OPS_DOMAIN_BASE", "example.org")
	if got := AppHostname("hello"); got != "hello.example.org" {
		t.Fatalf("AppHostname()=%q want hello.example.org", got)
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
	// ADR-0013 vars deliberately empty — must NOT trigger panic in dev.
	t.Setenv("APP_SOURCE_INGEST_ROOT", "")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "")
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

func TestAssertProductionSafePanicsOnWhitespaceIngestRoot(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "   ")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "x")
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for whitespace-only APP_SOURCE_INGEST_ROOT")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafePanicsOnWhitespaceBuildTokenSecret(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "/var/lib/0ops/uploads")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "\t  \n")
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for whitespace-only OPS_BUILD_TOKEN_SECRET")
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
