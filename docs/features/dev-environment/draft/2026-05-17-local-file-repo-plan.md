---
feature: dev-environment
sub_feature: local-file-repo
doc_type: plan
status: draft
date: 2026-05-17
source:
  - docs/features/dev-environment/local-file-repo.md
  - docs/adrs/0012-local-file-repo-dev-mode.md
task_id: M5.6
---

# Local-File-Repo Dev Mode (M5.6) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 `0ops apps create --repo-url file:///workspace/examples/node-demo` 在 `make dev` 環境完整跑通 preview → confirm → deploy_run `live`，無需 GitHub App / GHA / GHCR。

**Architecture:** sub-spec [`local-file-repo.md`](../local-file-repo.md) 已釘設計：env-gated 第三類 `repo_url` scheme + `Inspector` 介面 (LocalInspector / GitHubInspector) + `LocalBuildDispatcher` 實作既有 `createapp.Dispatcher` + `RoutingDispatcher` 依 `repo_url` 分派 + `OPS_ENV` 防呆。production 路徑與 ADR-0005 規約不動。

**Tech Stack:** Go (table-driven test) / paketo `pack` CLI / podman CLI / docker.io/library/registry:2 / podman compose v2 / chi / sqlc

---

## File Structure

**Create:**

| Path | Responsibility |
|---|---|
| `internal/shared/runtime/env.go` | `OPS_ENV` 解析 + `IsProduction()` |
| `internal/shared/runtime/env_test.go` | env 解析測試 |
| `internal/server/services/createapp/inspector.go` | `Inspector` 介面 + factory + `RepoMetadata` |
| `internal/server/services/createapp/github_inspector.go` | https://github.com/ scheme 之 inspector（包既有 stub 邏輯） |
| `internal/server/services/createapp/local_inspect.go` | file:// scheme 之 inspector |
| `internal/server/services/createapp/local_inspect_test.go` | tmp git repo fixture |
| `internal/server/services/createapp/routing_dispatcher.go` | RoutingDispatcher 包 GitHub + Local |
| `internal/server/services/createapp/routing_dispatcher_test.go` | 路由測試 |
| `internal/server/services/localbuild/dispatcher.go` | `LocalBuildDispatcher` 實作 `createapp.Dispatcher` |
| `internal/server/services/localbuild/dispatcher_test.go` | mock exec.Cmd + callback 序列 |
| `internal/server/services/localbuild/callback_client.go` | 對自家 server 打簽章 callback |
| `internal/server/services/localbuild/callback_client_test.go` | HMAC 簽章驗證 |
| `internal/server/services/localbuild/config.go` | env gate + `LOCAL_REGISTRY` / `LOCAL_FILE_REPO_ROOT` |
| `internal/server/services/localbuild/doc.go` | package doc |
| `examples/node-demo/package.json` | Express demo |
| `examples/node-demo/index.js` | hello server |
| `examples/node-demo/README.md` | 用法 |
| `examples/node-demo/.gitignore` | `node_modules/` |
| `examples/node-demo/bootstrap.sh` | git init + initial commit |
| `tasks/local-build-e2e.sh` | preview → confirm → poll live → 驗 image 存在 |

**Modify:**

| Path | Why |
|---|---|
| `internal/server/services/createapp/args.go:30-41` | `validateRepoURL` 加 file:// 分支 + env gate |
| `internal/server/services/createapp/service.go:265-286` | `validateRequest` 同步加 file:// 分支 + env gate |
| `internal/server/services/createapp/service.go:73-105` | `New()` 增 `Inspector` 與 `imageRefBuilder` 注入點（per § 6.4） |
| `internal/server/services/createapp/service.go:167` | `imageRef` 計算改走 `dispatcher.ImageRefFor()` 或 fallback hardcoded |
| `internal/server/db/apps.go` | 新增 `GetAppRepoURLByTeamAndAppSlug` 供 RoutingDispatcher |
| `internal/server/db/queries.sql` | 對應 sql |
| `internal/server/apps.go:1622-1628` | `newWorkflowDispatchClient()` 條件回 RoutingDispatcher |
| `internal/server/apps.go` | server boot：`runtime.AssertProductionSafe()` |
| `compose.yaml` | registry service + podman socket mount + 三個新 env + examples 掛載 |
| `compose.override.yaml.example` | dev 設定示範 |
| `.env.example` | 註記新 env（dev only） |
| `Makefile` | `dev-example-init`、`dev-create-example`、`m5-6-local-build-e2e` |
| `tasks/task-list.md` | 新增 M5.6 列 |
| `tasks/task-status.md` | 新增 M5.6 = Pending |
| `tasks/todo.md` | Milestone Supporting Work 加 group |
| `docs/features/dev-environment/local-file-repo.md` | 完成後 status: draft → accepted |

---

## Tasks

### Task 1: OPS_ENV runtime helper

**Files:**

- Create: `internal/shared/runtime/env.go`
- Create: `internal/shared/runtime/env_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/shared/runtime/env_test.go
package runtime

import (
	"testing"
)

func TestEnvKind(t *testing.T) {
	for _, tc := range []struct {
		raw         string
		wantKind    EnvKind
		wantProd    bool
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

func TestAssertProductionSafeNoopInDev(t *testing.T) {
	t.Setenv("OPS_ENV", "development")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	AssertProductionSafe() // must not panic
}
```

- [ ] **Step 2: Run test to verify failure**

```
go test ./internal/shared/runtime/...
```

Expected: FAIL (package not found)

- [ ] **Step 3: Implement env.go**

```go
// internal/shared/runtime/env.go
// Package runtime exposes process-wide environment kind detection and
// production safety assertions. Introduced by ADR-0012.
package runtime

import (
	"fmt"
	"os"
	"strings"
)

type EnvKind string

const (
	EnvDevelopment EnvKind = "development"
	EnvStaging     EnvKind = "staging"
	EnvProduction  EnvKind = "production"
)

// CurrentEnv reads OPS_ENV and returns the parsed kind. Unknown / empty
// values default to EnvDevelopment to keep local workflows working.
func CurrentEnv() EnvKind {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("OPS_ENV")))
	switch raw {
	case string(EnvProduction):
		return EnvProduction
	case string(EnvStaging):
		return EnvStaging
	default:
		return EnvDevelopment
	}
}

// IsProduction reports whether the current process is running in production.
func IsProduction() bool {
	return CurrentEnv() == EnvProduction
}

// AssertProductionSafe panics if any LOCAL_* dev knob is enabled while running
// in production. Called from server boot (cmd/server/main.go) to fail fast.
// Per ADR-0012 § 3.1 / sub-spec § 4.
func AssertProductionSafe() {
	if !IsProduction() {
		return
	}
	for _, key := range []string{
		"LOCAL_FILE_REPO_ENABLED",
		"LOCAL_BUILD_ENABLED",
	} {
		if envTrue(key) {
			panic(fmt.Sprintf("runtime: %s=true is forbidden when OPS_ENV=production (ADR-0012)", key))
		}
	}
	if strings.TrimSpace(os.Getenv("LOCAL_REGISTRY")) != "" {
		panic("runtime: LOCAL_REGISTRY must be unset when OPS_ENV=production (ADR-0012)")
	}
}

func envTrue(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}
```

- [ ] **Step 4: Verify pass**

```
go test ./internal/shared/runtime/...
```

Expected: PASS

- [ ] **Step 5: Wire AssertProductionSafe into server boot**

Modify `cmd/server/main.go` after env load, before chi router init:

```go
import "github.com/winshare/zeroops/internal/shared/runtime"
// ...
runtime.AssertProductionSafe()
```

(Exact insertion line depends on current main.go; add directly after slog setup.)

- [ ] **Step 6: Commit**

```bash
git add internal/shared/runtime/ cmd/server/main.go
git commit -m "feat: add OPS_ENV runtime kind + production safety assertion (ADR-0012)"
```

---

### Task 2: file:// validator with env gate

**Files:**

- Modify: `internal/server/services/createapp/args.go:30-41`
- Modify: `internal/server/services/createapp/service.go:265-286`
- Test: `internal/server/services/createapp/args_test.go` (existing)

- [ ] **Step 1: Add failing tests to args_test.go**

```go
func TestValidateRepoURL_FileScheme(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCAL_FILE_REPO_ROOT", root)

	// repo dir under root
	repoDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		gateOn    bool
		url       string
		wantError bool
	}{
		{"gate off rejects file://", false, "file://" + repoDir, true},
		{"gate on accepts in-root", true, "file://" + repoDir, false},
		{"gate on rejects escape", true, "file:///etc/passwd", true},
		{"gate on rejects non-absolute", true, "file://relative/path", true},
		{"gate on rejects missing path", true, "file:///tmp/does-not-exist-xyz", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gateOn {
				t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
			} else {
				t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
			}
			err := validateRepoURL(tc.url)
			if (err != nil) != tc.wantError {
				t.Fatalf("validateRepoURL(%q) err=%v wantError=%v", tc.url, err, tc.wantError)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify failure**

```
go test ./internal/server/services/createapp/ -run TestValidateRepoURL_FileScheme -v
```

Expected: FAIL (all file:// cases rejected by existing regex)

- [ ] **Step 3: Implement file:// branch in args.go**

Replace `validateRepoURL` body:

```go
// internal/server/services/createapp/args.go
package createapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	githubHTTPS = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+$`)
	githubSSH   = regexp.MustCompile(`^git@github\.com:[^/]+/[^/]+\.git$`)
)

// ErrLocalFileRepoDisabled, ErrRepoPathInvalid, ErrRepoPathNotFound are exposed
// so HTTP handlers can map to apperror codes.
var (
	ErrLocalFileRepoDisabled = errors.New("local file repo disabled")
	ErrRepoPathInvalid       = errors.New("repo path invalid")
	ErrRepoPathNotFound      = errors.New("repo path not found")
)

func validateRepoURL(repoURL string) error {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if strings.HasPrefix(repoURL, "file://") {
		return validateLocalRepoURL(repoURL)
	}
	if !githubHTTPS.MatchString(repoURL) && !githubSSH.MatchString(repoURL) {
		return fmt.Errorf("invalid repo_url")
	}
	return nil
}

func validateLocalRepoURL(repoURL string) error {
	if !localFileRepoEnabled() {
		return ErrLocalFileRepoDisabled
	}
	raw := strings.TrimPrefix(repoURL, "file://")
	if !filepath.IsAbs(raw) {
		return ErrRepoPathInvalid
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return ErrRepoPathNotFound
	}
	root := strings.TrimSpace(os.Getenv("LOCAL_FILE_REPO_ROOT"))
	if root == "" {
		return ErrRepoPathInvalid
	}
	rootResolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return ErrRepoPathInvalid
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return ErrRepoPathInvalid
	}
	return nil
}

func localFileRepoEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LOCAL_FILE_REPO_ENABLED")), "true")
}
```

- [ ] **Step 4: Mirror change in service.go validateRequest**

Replace `service.go:275-281`:

```go
	repoURL := strings.TrimSpace(req.RepoURL)
	if repoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if strings.HasPrefix(repoURL, "file://") {
		if err := validateLocalRepoURL(repoURL); err != nil {
			return err
		}
	} else if !strings.HasPrefix(repoURL, "https://github.com/") && !strings.HasPrefix(repoURL, "git@github.com:") {
		return fmt.Errorf("unsupported repo_url")
	}
```

- [ ] **Step 5: Verify pass + existing tests still green**

```
go test ./internal/server/services/createapp/...
```

Expected: PASS (new test green; existing service_test.go untouched because env gate defaults off)

- [ ] **Step 6: Commit**

```bash
git add internal/server/services/createapp/args.go internal/server/services/createapp/service.go internal/server/services/createapp/args_test.go
git commit -m "feat(createapp): accept file:// repo_url under LOCAL_FILE_REPO_ENABLED gate"
```

---

### Task 3: Inspector interface + LocalInspector

**Files:**

- Create: `internal/server/services/createapp/inspector.go`
- Create: `internal/server/services/createapp/github_inspector.go`
- Create: `internal/server/services/createapp/local_inspect.go`
- Create: `internal/server/services/createapp/local_inspect_test.go`

- [ ] **Step 1: Define interface and types in inspector.go**

```go
// internal/server/services/createapp/inspector.go
package createapp

import (
	"context"
	"strings"
)

// RepoMetadata is the inspection result consumed by preview side_effects
// and the deploy pipeline. Mirrors the spec § 5.1 step 4 contract.
type RepoMetadata struct {
	CommitSHA       string
	DefaultBranch   string
	Builder         string
	PrimaryPort     int
	GitHubAppStatus string // installed | installed_no_access | not_applicable
}

// Inspector resolves repo metadata given a repo URL and ref. Implementations
// are scheme-specific; see github_inspector.go / local_inspect.go.
type Inspector interface {
	Inspect(ctx context.Context, repoURL, ref string) (RepoMetadata, error)
}

// NewInspector returns the inspector matching the URL scheme.
func NewInspector(githubInspector, localInspector Inspector) Inspector {
	return inspectorRouter{github: githubInspector, local: localInspector}
}

type inspectorRouter struct {
	github Inspector
	local  Inspector
}

func (r inspectorRouter) Inspect(ctx context.Context, repoURL, ref string) (RepoMetadata, error) {
	if strings.HasPrefix(repoURL, "file://") {
		if r.local == nil {
			return RepoMetadata{}, ErrLocalFileRepoDisabled
		}
		return r.local.Inspect(ctx, repoURL, ref)
	}
	if r.github == nil {
		return RepoMetadata{GitHubAppStatus: "not_applicable"}, nil
	}
	return r.github.Inspect(ctx, repoURL, ref)
}
```

- [ ] **Step 2: Stub github_inspector.go (no behavior change vs current)**

```go
// internal/server/services/createapp/github_inspector.go
package createapp

import "context"

// GitHubInspector preserves the current pre-sub-spec behavior: returns an
// empty metadata; the real GitHub App + paketo detect call is tracked
// separately under inspect_repo handler refactor.
type GitHubInspector struct{}

func (GitHubInspector) Inspect(_ context.Context, _ string, _ string) (RepoMetadata, error) {
	return RepoMetadata{GitHubAppStatus: "not_applicable"}, nil
}
```

- [ ] **Step 3: Write failing test for LocalInspector**

```go
// internal/server/services/createapp/local_inspect_test.go
package createapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@local", "-c", "user.name=t", "add", "."},
		{"-c", "user.email=t@local", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestLocalInspector_Node(t *testing.T) {
	dir := setupGitRepo(t, map[string]string{
		"package.json": `{"name":"x","engines":{"node":"20.x"}}`,
	})
	t.Setenv("LOCAL_FILE_REPO_ROOT", dir)
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")

	got, err := LocalInspector{}.Inspect(context.Background(), "file://"+dir, "")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got.DefaultBranch != "main" {
		t.Errorf("default_branch=%q want main", got.DefaultBranch)
	}
	if got.Builder != "paketobuildpacks/builder-jammy-base" {
		t.Errorf("builder=%q", got.Builder)
	}
	if got.PrimaryPort != 3000 {
		t.Errorf("port=%d want 3000", got.PrimaryPort)
	}
	if len(got.CommitSHA) != 40 {
		t.Errorf("commit_sha=%q must be 40 hex", got.CommitSHA)
	}
	if got.GitHubAppStatus != "not_applicable" {
		t.Errorf("github status=%q", got.GitHubAppStatus)
	}
}

func TestLocalInspector_DetectFailed(t *testing.T) {
	dir := setupGitRepo(t, map[string]string{"README.md": "no code"})
	t.Setenv("LOCAL_FILE_REPO_ROOT", dir)
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "true")
	_, err := LocalInspector{}.Inspect(context.Background(), "file://"+dir, "")
	if err == nil || err.Error() == "" {
		t.Fatalf("expected detect failure, got %v", err)
	}
}
```

- [ ] **Step 4: Run test to verify failure**

```
go test ./internal/server/services/createapp/ -run TestLocalInspector -v
```

Expected: FAIL (LocalInspector undefined)

- [ ] **Step 5: Implement LocalInspector**

```go
// internal/server/services/createapp/local_inspect.go
package createapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalInspector inspects a file:// repo by reading the local filesystem and
// invoking `git` for HEAD / default branch. Paketo language detection is a
// minimal heuristic mirroring spec § 5.3.
type LocalInspector struct{}

var ErrBuildpackDetectFailed = errors.New("buildpack_detect_failed")

func (LocalInspector) Inspect(ctx context.Context, repoURL, _ string) (RepoMetadata, error) {
	if err := validateLocalRepoURL(repoURL); err != nil {
		return RepoMetadata{}, err
	}
	path := strings.TrimPrefix(repoURL, "file://")
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return RepoMetadata{}, ErrRepoPathNotFound
	}
	if _, err := os.Stat(filepath.Join(resolved, ".git")); err != nil {
		return RepoMetadata{}, fmt.Errorf("repo_not_git")
	}
	commit, err := runGit(ctx, resolved, "rev-parse", "HEAD")
	if err != nil {
		return RepoMetadata{}, fmt.Errorf("git rev-parse: %w", err)
	}
	branch, err := runGit(ctx, resolved, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "HEAD" {
		branch = "main"
	}
	builder, port, ok := detectPaketo(resolved)
	if !ok {
		return RepoMetadata{}, ErrBuildpackDetectFailed
	}
	return RepoMetadata{
		CommitSHA:       commit,
		DefaultBranch:   branch,
		Builder:         builder,
		PrimaryPort:     port,
		GitHubAppStatus: "not_applicable",
	}, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// detectPaketo is intentionally minimal — see ADR-0012 § 7 Revisit Triggers
// for the matrix expansion plan.
func detectPaketo(dir string) (string, int, bool) {
	switch {
	case exists(dir, "package.json"):
		return "paketobuildpacks/builder-jammy-base", 3000, true
	case exists(dir, "go.mod"):
		return "paketobuildpacks/builder-jammy-base", 8080, true
	case exists(dir, "pyproject.toml"), exists(dir, "requirements.txt"):
		return "paketobuildpacks/builder-jammy-base", 8000, true
	}
	return "", 0, false
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
```

- [ ] **Step 6: Verify pass**

```
go test ./internal/server/services/createapp/...
```

Expected: PASS (Node + DetectFailed paths green; existing tests untouched)

- [ ] **Step 7: Commit**

```bash
git add internal/server/services/createapp/inspector.go internal/server/services/createapp/github_inspector.go internal/server/services/createapp/local_inspect.go internal/server/services/createapp/local_inspect_test.go
git commit -m "feat(createapp): add Inspector interface + LocalInspector for file:// repos"
```

---

### Task 4: examples/node-demo

**Files:**

- Create: `examples/node-demo/package.json`
- Create: `examples/node-demo/index.js`
- Create: `examples/node-demo/README.md`
- Create: `examples/node-demo/.gitignore`
- Create: `examples/node-demo/bootstrap.sh`

- [ ] **Step 1: Write package.json**

```json
{
  "name": "node-demo",
  "version": "0.1.0",
  "description": "0ops example app for local-file-repo dev mode (ADR-0012).",
  "engines": { "node": "20.x" },
  "scripts": { "start": "node index.js" },
  "dependencies": { "express": "^4.19.2" }
}
```

- [ ] **Step 2: Write index.js**

```js
const express = require("express");
const app = express();
const port = process.env.PORT || 3000;

app.get("/", (_req, res) => res.send("hello from 0ops node-demo\n"));
app.get("/healthz", (_req, res) => res.json({ ok: true }));

app.listen(port, () => console.log("node-demo listening on " + port));
```

- [ ] **Step 3: Write .gitignore**

```
node_modules/
```

- [ ] **Step 4: Write README.md**

```markdown
# node-demo

0ops 之 example repo，用於 `local-file-repo` dev mode（ADR-0012）。
Paketo NodeJS buildpack 直接偵測；無 production 用途。

## 使用

從 0ops repo 根目錄：

    make dev-create-example

或手動：

    bash examples/node-demo/bootstrap.sh     # git init + initial commit
    0ops apps create --repo-url file:///workspace/examples/node-demo --ref main --slug node-demo --yes

驗證：

    0ops deploys status node-demo            # 期望 live
    curl http://localhost:5000/v2/0ops-apps/personal/node-demo/tags/list
```

- [ ] **Step 5: Write bootstrap.sh**

```sh
#!/usr/bin/env bash
# Initialize examples/node-demo as a git repository so the file:// inspector
# (per ADR-0012 § 5.3) can read HEAD / default branch.
set -euo pipefail
cd "$(dirname "$0")"
if [ -d .git ]; then
  echo "examples/node-demo already initialized"
  exit 0
fi
git init -q -b main
git -c user.email=dev@0ops.local -c user.name=dev add .
git -c user.email=dev@0ops.local -c user.name=dev commit -q -m "initial node-demo"
echo "examples/node-demo initialized as git repo"
```

- [ ] **Step 6: Make bootstrap executable + smoke test**

```bash
chmod +x examples/node-demo/bootstrap.sh
bash examples/node-demo/bootstrap.sh
git -C examples/node-demo log --oneline
```

Expected output: one commit "initial node-demo".

- [ ] **Step 7: Decide example repo's relation to outer git**

The outer 0ops repo will track these files. The inner `.git` directory must NOT be tracked. Add to outer repo `.gitignore`:

```
examples/*/.git/
```

(append; do not replace existing entries).

- [ ] **Step 8: Commit**

```bash
git add examples/node-demo/ .gitignore
git commit -m "feat: add examples/node-demo Express app + bootstrap (ADR-0012)"
```

---

### Task 5: localbuild package — config + callback client

**Files:**

- Create: `internal/server/services/localbuild/config.go`
- Create: `internal/server/services/localbuild/callback_client.go`
- Create: `internal/server/services/localbuild/callback_client_test.go`
- Create: `internal/server/services/localbuild/doc.go`

- [ ] **Step 1: Write doc.go**

```go
// Package localbuild provides a dev-only implementation of
// createapp.Dispatcher that runs paketo `pack build` against a local file://
// repo and reports state transitions back via the existing callback handler.
// Per ADR-0012; never enabled in production.
package localbuild
```

- [ ] **Step 2: Write config.go**

```go
// internal/server/services/localbuild/config.go
package localbuild

import (
	"os"
	"strings"
)

type Config struct {
	Enabled      bool
	Registry     string // e.g. localhost:5000
	RepoRoot     string // e.g. /workspace/examples
	CallbackBase string // e.g. http://localhost:8080
	Secret       string // shared with production callback HMAC
}

func LoadConfig() Config {
	return Config{
		Enabled:      envTrue("LOCAL_BUILD_ENABLED"),
		Registry:     strings.TrimSpace(os.Getenv("LOCAL_REGISTRY")),
		RepoRoot:     strings.TrimSpace(os.Getenv("LOCAL_FILE_REPO_ROOT")),
		CallbackBase: strings.TrimSpace(os.Getenv("OPS_PUBLIC_BASE_URL")),
		Secret:       strings.TrimSpace(os.Getenv("OPS_CALLBACK_SECRET")),
	}
}

func envTrue(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}

func (c Config) IsUsable() bool {
	return c.Enabled && c.Registry != "" && c.RepoRoot != "" && c.Secret != ""
}
```

- [ ] **Step 3: Write failing test for callback_client**

```go
// internal/server/services/localbuild/callback_client_test.go
package localbuild

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallbackClientSignsAndPosts(t *testing.T) {
	secret := "dev-callback-secret-change-me"
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Ops-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCallbackClient(srv.URL, secret, http.DefaultClient)
	if err := c.Send(context.Background(), "dr_test", CallbackEvent{Status: "building"}); err != nil {
		t.Fatal(err)
	}

	var ev CallbackEvent
	if err := json.Unmarshal(gotBody, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Status != "building" {
		t.Errorf("status=%q", ev.Status)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	expected := "hmac-sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != expected {
		t.Errorf("sig mismatch:\n got %s\nwant %s", gotSig, expected)
	}
}
```

- [ ] **Step 4: Run test to verify failure**

```
go test ./internal/server/services/localbuild/ -run TestCallbackClient -v
```

Expected: FAIL

- [ ] **Step 5: Implement callback_client.go**

```go
// internal/server/services/localbuild/callback_client.go
package localbuild

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type CallbackEvent struct {
	Status                string   `json:"status"`
	ImageRef              string   `json:"image_ref,omitempty"`
	BuildMinutes          float64  `json:"build_minutes,omitempty"`
	ErrorSummary          string   `json:"error_summary,omitempty"`
	FailureClassification string   `json:"failure_classification,omitempty"`
}

type CallbackClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewCallbackClient(baseURL, secret string, c *http.Client) *CallbackClient {
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	return &CallbackClient{baseURL: strings.TrimRight(baseURL, "/"), secret: secret, http: c}
}

func (c *CallbackClient) Send(ctx context.Context, runID string, ev CallbackEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/internal/deploy-runs/%s/callback", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ops-Run-Id", runID)
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write(body)
	req.Header.Set("X-Ops-Signature", "hmac-sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("callback %s: %d", url, resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 6: Verify pass**

```
go test ./internal/server/services/localbuild/...
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/services/localbuild/
git commit -m "feat(localbuild): add config + signed callback client (ADR-0012)"
```

---

### Task 6: LocalBuildDispatcher

**Files:**

- Create: `internal/server/services/localbuild/dispatcher.go`
- Create: `internal/server/services/localbuild/dispatcher_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/server/services/localbuild/dispatcher_test.go
package localbuild

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

type recCallback struct {
	mu     sync.Mutex
	events []CallbackEvent
}

func (r *recCallback) Send(_ context.Context, _ string, ev CallbackEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func TestLocalBuildDispatcherHappyPath(t *testing.T) {
	rec := &recCallback{}
	d := &Dispatcher{
		Pack:     func(ctx context.Context, imageRef, path, builder string) error { return nil },
		Callback: rec,
	}
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		RunID: "dr_1", AppSlug: "x", TeamSlug: "t",
	}); err != nil {
		t.Fatal(err)
	}
	d.WaitForIdle()

	want := []string{"building", "pushing", "rendering", "syncing", "live"}
	if len(rec.events) != len(want) {
		t.Fatalf("events=%d want %d (%v)", len(rec.events), len(want), rec.events)
	}
	for i, s := range want {
		if rec.events[i].Status != s {
			t.Errorf("events[%d]=%q want %q", i, rec.events[i].Status, s)
		}
	}
}

func TestLocalBuildDispatcherBuildFailure(t *testing.T) {
	rec := &recCallback{}
	d := &Dispatcher{
		Pack:     func(ctx context.Context, imageRef, path, builder string) error { return errors.New("oom") },
		Callback: rec,
	}
	if err := d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "dr_x"}); err != nil {
		t.Fatal(err)
	}
	d.WaitForIdle()

	if len(rec.events) < 2 {
		t.Fatalf("expected at least 2 events, got %v", rec.events)
	}
	last := rec.events[len(rec.events)-1]
	if last.Status != "failed" || last.FailureClassification != "build_error" {
		t.Errorf("last event=%+v", last)
	}
	if !strings.Contains(last.ErrorSummary, "oom") {
		t.Errorf("error_summary=%q", last.ErrorSummary)
	}
}

// Sanity: integration with real http callback (HMAC path covered separately
// in callback_client_test).
func TestLocalBuildDispatcherUsesCallbackHTTP(t *testing.T) {
	got := make(chan CallbackEvent, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev CallbackEvent
		_ = (&ev).fromJSON(body)
		got <- ev
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cc := NewCallbackClient(srv.URL, "k", nil)
	d := &Dispatcher{
		Pack:     func(_ context.Context, _, _, _ string) error { return nil },
		Callback: cc,
	}
	_ = d.Dispatch(context.Background(), workflowdispatch.ClientPayload{RunID: "dr_h"})
	select {
	case ev := <-got:
		if ev.Status == "" {
			t.Fatal("empty status")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for callback")
	}
}

func (e *CallbackEvent) fromJSON(b []byte) error {
	return jsonUnmarshal(b, e)
}
```

(Add a small helper to package for unmarshalling in test or inline `json.Unmarshal`. For brevity, replace `jsonUnmarshal` with `json.Unmarshal` and add the import.)

- [ ] **Step 2: Run test to verify failure**

```
go test ./internal/server/services/localbuild/ -run TestLocalBuildDispatcher -v
```

Expected: FAIL

- [ ] **Step 3: Implement dispatcher.go**

```go
// internal/server/services/localbuild/dispatcher.go
package localbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// CallbackSender abstracts the HTTP callback so tests can inject a recorder.
type CallbackSender interface {
	Send(ctx context.Context, runID string, ev CallbackEvent) error
}

// AppLookup resolves the local filesystem path + builder for a given run.
// In production this is db-backed; in tests it can be a closure.
type AppLookup interface {
	ResolveLocalPath(ctx context.Context, teamSlug, appSlug string) (path, builder string, err error)
}

// PackFunc is the abstraction over `pack build --publish ... --path ...`.
type PackFunc func(ctx context.Context, imageRef, path, builder string) error

// Dispatcher implements createapp.Dispatcher for file:// repos.
type Dispatcher struct {
	Pack     PackFunc
	Callback CallbackSender
	Lookup   AppLookup
	Registry string

	wg sync.WaitGroup
}

// Dispatch fires the local build in a goroutine; returns immediately to mirror
// production GHA workflow_dispatch semantics.
func (d *Dispatcher) Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.run(context.Background(), payload) // detached context; HTTP req ctx may cancel
	}()
	return nil
}

// WaitForIdle blocks until all dispatched goroutines finish. Test-only helper.
func (d *Dispatcher) WaitForIdle() { d.wg.Wait() }

func (d *Dispatcher) run(ctx context.Context, payload workflowdispatch.ClientPayload) {
	send := func(ev CallbackEvent) {
		_ = d.Callback.Send(ctx, payload.RunID, ev)
	}

	send(CallbackEvent{Status: "building"})

	path, builder := "", ""
	if d.Lookup != nil {
		p, b, err := d.Lookup.ResolveLocalPath(ctx, payload.TeamSlug, payload.AppSlug)
		if err != nil {
			send(CallbackEvent{
				Status:                "failed",
				FailureClassification: "build_error",
				ErrorSummary:          fmt.Sprintf("resolve local path: %v", err),
			})
			return
		}
		path, builder = p, b
	}

	imageRef := payload.ImageRef
	if d.Pack != nil {
		if err := d.Pack(ctx, imageRef, path, builder); err != nil {
			send(CallbackEvent{
				Status:                "failed",
				FailureClassification: "build_error",
				ErrorSummary:          truncate(err.Error(), 8192),
			})
			return
		}
	}

	for _, s := range []string{"pushing", "rendering", "syncing", "live"} {
		ev := CallbackEvent{Status: s}
		if s == "pushing" {
			ev.ImageRef = imageRef
		}
		send(ev)
		time.Sleep(50 * time.Millisecond) // small spacing for SSE log tail
	}
}

// DefaultPack runs `pack build --publish <imageRef> --path <path> --builder <builder>`.
func DefaultPack(ctx context.Context, imageRef, path, builder string) error {
	if imageRef == "" || path == "" || builder == "" {
		return errors.New("missing pack arguments")
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "pack", "build", "--publish", imageRef, "--path", path, "--builder", builder)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(buf.String()))
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
```

Replace the test helper `jsonUnmarshal` with `encoding/json.Unmarshal` directly:

```go
// in dispatcher_test.go imports
import "encoding/json"
// replace fromJSON helper:
func (e *CallbackEvent) fromJSON(b []byte) error { return json.Unmarshal(b, e) }
```

- [ ] **Step 4: Verify pass**

```
go test ./internal/server/services/localbuild/...
```

Expected: PASS (3 sub-tests).

- [ ] **Step 5: Commit**

```bash
git add internal/server/services/localbuild/dispatcher.go internal/server/services/localbuild/dispatcher_test.go
git commit -m "feat(localbuild): add Dispatcher with pack-build + callback chain (ADR-0012)"
```

---

### Task 7: RoutingDispatcher + db query + apps.go wiring

**Files:**

- Create: `internal/server/services/createapp/routing_dispatcher.go`
- Create: `internal/server/services/createapp/routing_dispatcher_test.go`
- Create: `internal/server/services/localbuild/lookup.go`
- Modify: `internal/server/db/apps.go` (append `GetAppRepoURLByTeamAndAppSlug`)
- Modify: `internal/server/db/queries_test.go` (append db smoke test)
- Modify: `internal/server/apps.go:1622-1628` (wire RoutingDispatcher)

- [ ] **Step 1: Add db method (with smoke test)**

Append to `internal/server/db/apps.go`:

```go
// GetAppRepoURLByTeamAndAppSlug fetches just the repo_url for routing
// dispatchers (per ADR-0012 § 3.2). Returns pgx.ErrNoRows if not found.
func (r *Repository) GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error) {
	const q = `
SELECT a.repo_url
  FROM app a
  JOIN team t ON t.id = a.team_id
 WHERE t.slug = $1 AND a.slug = $2
 LIMIT 1`
	var url string
	if err := r.pool.QueryRow(ctx, q, teamSlug, appSlug).Scan(&url); err != nil {
		return "", err
	}
	return url, nil
}
```

- [ ] **Step 2: Add db smoke test**

Append to `internal/server/db/queries_test.go` (or create new test file):

```go
func TestGetAppRepoURLByTeamAndAppSlug(t *testing.T) {
	t.Parallel()
	repo, cleanup := newRepoT(t) // existing helper in queries_test.go
	defer cleanup()
	ctx := context.Background()

	// create team + app (use existing test helpers; pseudo:)
	team := mustCreateTeam(t, repo, "demo-team")
	mustCreateApp(t, repo, team.ID, "demo-app", "file:///workspace/examples/node-demo")

	got, err := repo.GetAppRepoURLByTeamAndAppSlug(ctx, "demo-team", "demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if got != "file:///workspace/examples/node-demo" {
		t.Errorf("repo_url=%q", got)
	}
}
```

(Adapt `mustCreateTeam` / `mustCreateApp` to existing helpers in `queries_test.go`; if absent, inline raw INSERTs.)

- [ ] **Step 3: Run db test to verify**

```
podman compose up -d db
go test ./internal/server/db/... -run TestGetAppRepoURL -v
```

Expected: PASS

- [ ] **Step 4: Write failing test for RoutingDispatcher**

```go
// internal/server/services/createapp/routing_dispatcher_test.go
package createapp

import (
	"context"
	"errors"
	"testing"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

type recDispatcher struct{ called string }

func (r *recDispatcher) Dispatch(_ context.Context, _ workflowdispatch.ClientPayload) error {
	r.called = "ok"
	return nil
}

type fakeRepoLookup struct{ url string; err error }

func (f fakeRepoLookup) GetAppRepoURLByTeamAndAppSlug(_ context.Context, _, _ string) (string, error) {
	return f.url, f.err
}

func TestRoutingDispatcher_RoutesByScheme(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		err       error
		wantLocal bool
		wantErr   bool
	}{
		{"github", "https://github.com/x/y", nil, false, false},
		{"file", "file:///workspace/examples/node-demo", nil, true, false},
		{"lookup error", "", errors.New("not found"), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := &recDispatcher{}
			gh := &recDispatcher{}
			rd := &RoutingDispatcher{
				GitHubDispatcher: gh,
				LocalDispatcher:  local,
				Lookup:           fakeRepoLookup{url: tc.url, err: tc.err},
			}
			err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{TeamSlug: "t", AppSlug: "a"})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLocal && local.called != "ok" {
				t.Errorf("local not called")
			}
			if !tc.wantLocal && gh.called != "ok" {
				t.Errorf("github not called")
			}
		})
	}
}
```

- [ ] **Step 5: Run test to verify failure**

```
go test ./internal/server/services/createapp/ -run TestRoutingDispatcher -v
```

Expected: FAIL

- [ ] **Step 6: Implement routing_dispatcher.go**

```go
// internal/server/services/createapp/routing_dispatcher.go
package createapp

import (
	"context"
	"strings"

	"github.com/winshare/zeroops/internal/server/services/workflowdispatch"
)

// RepoURLLookup resolves an app's repo_url for routing.
type RepoURLLookup interface {
	GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error)
}

// RoutingDispatcher selects between GitHub and Local dispatchers based on the
// stored repo_url. Per ADR-0012 § 3.2 the workflowdispatch.ClientPayload
// contract is preserved (no extra fields).
type RoutingDispatcher struct {
	GitHubDispatcher Dispatcher
	LocalDispatcher  Dispatcher
	Lookup           RepoURLLookup
}

func (r *RoutingDispatcher) Dispatch(ctx context.Context, payload workflowdispatch.ClientPayload) error {
	url, err := r.Lookup.GetAppRepoURLByTeamAndAppSlug(ctx, payload.TeamSlug, payload.AppSlug)
	if err != nil {
		return err
	}
	if strings.HasPrefix(url, "file://") && r.LocalDispatcher != nil {
		return r.LocalDispatcher.Dispatch(ctx, payload)
	}
	if r.GitHubDispatcher == nil {
		return nil // nil-tolerant (matches existing behaviour when env unset)
	}
	return r.GitHubDispatcher.Dispatch(ctx, payload)
}
```

- [ ] **Step 7: Verify pass**

```
go test ./internal/server/services/createapp/...
```

Expected: PASS

- [ ] **Step 8: Wire RoutingDispatcher in apps.go**

Replace `newWorkflowDispatchClient()` body (apps.go:1622-1628) with:

```go
func newWorkflowDispatchClient(store appsStore) createappsvc.Dispatcher {
	ghClient, _ := workflowdispatch.NewClientFromEnv(http.DefaultClient)
	cfg := localbuild.LoadConfig()
	if !cfg.IsUsable() {
		// production path unchanged.
		if ghClient == nil {
			return nil
		}
		return ghClient
	}
	// dev path: route by repo_url scheme.
	cb := localbuild.NewCallbackClient(callbackBaseURL(), cfg.Secret, http.DefaultClient)
	localDispatcher := &localbuild.Dispatcher{
		Pack:     localbuild.DefaultPack,
		Callback: cb,
		Lookup:   localbuild.RepoRootLookup{Store: store, Root: cfg.RepoRoot},
		Registry: cfg.Registry,
	}
	return &createappsvc.RoutingDispatcher{
		GitHubDispatcher: dispatcherOrNil(ghClient),
		LocalDispatcher:  localDispatcher,
		Lookup:           store,
	}
}

func dispatcherOrNil(c *workflowdispatch.Client) createappsvc.Dispatcher {
	if c == nil { return nil }
	return c
}
```

Create `internal/server/services/localbuild/lookup.go`:

```go
// internal/server/services/localbuild/lookup.go
package localbuild

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// RepoURLStore is the minimal store contract RepoRootLookup needs.
type RepoURLStore interface {
	GetAppRepoURLByTeamAndAppSlug(ctx context.Context, teamSlug, appSlug string) (string, error)
}

// RepoRootLookup resolves a stored repo_url to a local filesystem path,
// re-validating against the configured root. It closes the TOCTOU window
// between preview (createapp.validateLocalRepoURL) and confirm:
// preview-time validation does not block a malicious actor from racing
// a repo_url swap, so the dispatcher (an independent trust boundary)
// must re-validate.
type RepoRootLookup struct {
	Store RepoURLStore
	Root  string
}

func (l RepoRootLookup) ResolveLocalPath(ctx context.Context, teamSlug, appSlug string) (string, string, error) {
	url, err := l.Store.GetAppRepoURLByTeamAndAppSlug(ctx, teamSlug, appSlug)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(url, "file://") {
		return "", "", errors.New("repo_url not file://")
	}
	path := strings.TrimPrefix(url, "file://")
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", err
	}
	rootResolved, err := filepath.EvalSymlinks(filepath.Clean(l.Root))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", errors.New("repo path escapes root")
	}
	// Paketo heuristic mirrors createapp.LocalInspector.detectPaketo;
	// node-demo is the M5.6 e2e fixture so jammy-base is sufficient.
	// Multi-language matrix expansion tracked in ADR-0012 § 9 Open Q #5.
	return resolved, "paketobuildpacks/builder-jammy-base", nil
}
```

Update `apps.go` call sites that previously invoked `newWorkflowDispatchClient()` with zero args — pass `store`.

- [ ] **Step 9: go build verify wiring compiles**

```
go build ./...
```

Expected: no errors.

- [ ] **Step 10: Commit**

```bash
git add internal/server/db/apps.go internal/server/db/queries_test.go internal/server/services/createapp/routing_dispatcher.go internal/server/services/createapp/routing_dispatcher_test.go internal/server/services/localbuild/dispatcher.go internal/server/apps.go
git commit -m "feat(localbuild): wire RoutingDispatcher in server boot (ADR-0012 § 3.2)"
```

---

### Task 8: compose + Makefile wiring

**Files:**

- Modify: `compose.yaml`
- Modify: `compose.override.yaml.example`
- Modify: `.env.example`
- Modify: `Makefile`

- [ ] **Step 1: Add registry service to compose.yaml**

```yaml
  registry:
    image: docker.io/library/registry:2
    ports:
      - "5000:5000"
    healthcheck:
      test: ["CMD", "wget", "-q", "-O-", "http://localhost:5000/v2/"]
      interval: 10s
      timeout: 3s
      retries: 5
```

- [ ] **Step 2: Extend server service env + volumes**

Under `services.server`:

```yaml
    environment:
      # ... existing ...
      OPS_ENV: ${OPS_ENV:-development}
      LOCAL_FILE_REPO_ENABLED: ${LOCAL_FILE_REPO_ENABLED:-true}
      LOCAL_BUILD_ENABLED: ${LOCAL_BUILD_ENABLED:-true}
      LOCAL_REGISTRY: ${LOCAL_REGISTRY:-registry:5000}
      LOCAL_FILE_REPO_ROOT: /workspace/examples
    volumes:
      - .:/app:Z
      - go-mod-cache:/go/pkg/mod
      - ./examples:/workspace/examples:Z
      - /run/user/${UID:-1000}/podman/podman.sock:/var/run/podman/podman.sock:Z
    depends_on:
      migrate:
        condition: service_completed_successfully
      registry:
        condition: service_healthy
```

Note: `LOCAL_REGISTRY` defaults to `registry:5000` (compose DNS) so server-internal HTTP probes work; `pack build --publish` invoked via host podman socket sees `localhost:5000` from host viewpoint, which maps to the same registry container. Document in `compose.override.yaml.example` if user needs host-side override.

- [ ] **Step 3: Update .env.example**

Append:

```
# Local-file-repo dev mode (ADR-0012). Production must leave OPS_ENV=production
# and these three blank.
OPS_ENV=development
LOCAL_FILE_REPO_ENABLED=true
LOCAL_BUILD_ENABLED=true
LOCAL_REGISTRY=registry:5000
```

- [ ] **Step 4: Add Makefile targets**

Append to Makefile:

```make
## --- local-file-repo dev mode (ADR-0012) ---

.PHONY: dev-example-init dev-create-example m5-6-local-build-e2e

dev-example-init: ## 初始化 examples/node-demo 為 git repo
	bash examples/node-demo/bootstrap.sh

dev-create-example: dev-example-init ## 跑一次 create_app at file:// → live
	bash tasks/local-build-e2e.sh

m5-6-local-build-e2e: ## M5.6 e2e 驗收（compose 必須先 healthy）
	bash tasks/local-build-e2e.sh
```

- [ ] **Step 5: Verify compose syntax**

```
make lint-compose
```

Expected: `podman compose config -q` exits 0.

- [ ] **Step 6: Commit**

```bash
git add compose.yaml compose.override.yaml.example .env.example Makefile
git commit -m "feat(compose): add registry + podman socket mount + LOCAL_* env (ADR-0012)"
```

---

### Task 9: e2e script

**Files:**

- Create: `tasks/local-build-e2e.sh`

- [ ] **Step 1: Write script**

```sh
#!/usr/bin/env bash
# tasks/local-build-e2e.sh — M5.6 acceptance script.
# Exercises: file:// preview → confirm → poll deploy_run live → verify
# image exists in local registry.
# Per ADR-0012 / sub-spec § 9.3.
set -euo pipefail

REGISTRY="${LOCAL_REGISTRY_HOST:-localhost:5000}"
TEAM_SLUG="${TEAM_SLUG:-personal}"
APP_SLUG="${APP_SLUG:-node-demo}"
REPO_URL="${REPO_URL:-file:///workspace/examples/node-demo}"
HOST="${OPS_HOST:-http://localhost:8080}"
TIMEOUT_SECS="${TIMEOUT_SECS:-120}"

log() { echo "[e2e] $*" >&2; }

log "1. ensure compose up"
podman compose up -d

log "2. wait for server healthy"
for i in $(seq 1 30); do
  if curl -fsS "$HOST/health" >/dev/null 2>&1; then break; fi
  sleep 2
  if [ "$i" = "30" ]; then echo "server not healthy" >&2; exit 1; fi
done

log "3. ensure example repo is git-initialized"
bash examples/node-demo/bootstrap.sh

log "4. mint dev token via seed-cli-token"
TOKEN="$(go run ./cmd/devtools/seed-cli-token --team "$TEAM_SLUG" --quiet)"
export OPS_TOKEN="$TOKEN"

log "5. create app with --yes (preview + confirm)"
go run ./cmd/cli apps create \
  --host "$HOST" --team "$TEAM_SLUG" --token "$TOKEN" \
  --slug "$APP_SLUG" --repo-url "$REPO_URL" --ref main --yes

log "6. poll deploy_run until live or timeout"
deadline=$(( $(date +%s) + TIMEOUT_SECS ))
while :; do
  status="$(go run ./cmd/cli deploys status "$APP_SLUG" \
    --host "$HOST" --team "$TEAM_SLUG" --token "$TOKEN" --output json | jq -r .status)"
  log "  status=$status"
  case "$status" in
    live) break ;;
    failed|rolled_back|failed_permanently|canceled)
      echo "terminal failure: $status" >&2; exit 1 ;;
  esac
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "timeout waiting for live" >&2; exit 1
  fi
  sleep 3
done

log "7. verify image exists in local registry"
TAGS_URL="http://${REGISTRY}/v2/0ops-apps/${TEAM_SLUG}/${APP_SLUG}/tags/list"
if ! curl -fsS "$TAGS_URL" | jq -e '.tags | length > 0' >/dev/null; then
  echo "image not found in registry at $TAGS_URL" >&2; exit 1
fi

log "OK — node-demo deploy_run reached live and image is present"
```

- [ ] **Step 2: chmod and dry-run syntax check**

```bash
chmod +x tasks/local-build-e2e.sh
bash -n tasks/local-build-e2e.sh
```

Expected: no output (syntax OK).

- [ ] **Step 3: Commit**

```bash
git add tasks/local-build-e2e.sh
git commit -m "feat(tasks): add M5.6 local-build e2e acceptance script (ADR-0012)"
```

---

### Task 10: full e2e run + docs alignment

**Files:**

- Modify: `tasks/task-list.md`
- Modify: `tasks/task-status.md`
- Modify: `tasks/todo.md`
- Modify: `docs/features/dev-environment/local-file-repo.md` (status accepted)
- Modify: `tasks/lessons.md`

- [ ] **Step 1: Run full e2e end-to-end**

```bash
make dev-clean
make dev
make m5-6-local-build-e2e
```

Expected: script prints `OK — node-demo deploy_run reached live and image is present`.

- [ ] **Step 2: Add M5.6 to task-list.md**

Append a new row in `tasks/task-list.md`:

```
| M5.6  | Local file repo + local build pipeline (dev mode) | M2.1, M2.2     | docs/features/dev-environment/local-file-repo.md, docs/adrs/0012-local-file-repo-dev-mode.md | `internal/server/services/createapp/**`, `internal/server/services/localbuild/**`, `examples/node-demo/**`, `tasks/local-build-e2e.sh` |
```

- [ ] **Step 3: Add M5.6 to task-status.md**

Append:

```
| M5.6  | Local file repo + local build pipeline (dev mode) | Done   | 2026-05-17     |
```

- [ ] **Step 4: Add M5.6 group to todo.md Milestone Supporting Work**

Append under `## Milestone Supporting Work`:

```markdown
### M5.6 — local-file-repo dev mode

- [x] OPS_ENV runtime helper + production safety assertion
- [x] file:// validator + LOCAL_FILE_REPO_ENABLED gate
- [x] Inspector interface + LocalInspector + GitHubInspector stub
- [x] examples/node-demo + bootstrap
- [x] localbuild package: config + signed callback client
- [x] LocalBuildDispatcher with pack-build + state chain
- [x] RoutingDispatcher + GetAppRepoURLByTeamAndAppSlug + apps.go wiring
- [x] compose registry + podman socket mount + Makefile targets
- [x] tasks/local-build-e2e.sh acceptance script
- [x] docs alignment + lessons capture
```

- [ ] **Step 5: Update sub-spec status**

In `docs/features/dev-environment/local-file-repo.md` frontmatter row 2:

```
> **狀態**：accepted
```

- [ ] **Step 6: Append a lesson to tasks/lessons.md**

```markdown
### M5.6 — local-file-repo dev mode

- Dispatcher 介面在 dev / production 共用時，RoutingDispatcher 反查 repo_url
  比擴 ClientPayload schema 更不破壞契約（ADR-0012 § 3.2）。
- podman socket mount 為 high-trust；server boot 加 AssertProductionSafe()
  panic 比靠 CI lint 阻止誤啟用更可靠。
- paketo `pack build --publish` 直接 push 省掉「build → push」中間 race；
  callback "pushing" 改為紀錄性 transition，不再對應實際動作。
```

- [ ] **Step 7: Final commit**

```bash
git add tasks/task-list.md tasks/task-status.md tasks/todo.md tasks/lessons.md docs/features/dev-environment/local-file-repo.md
git commit -m "chore(tasks): mark M5.6 local-file-repo dev mode Done + sub-spec accepted"
```

---

## Verification Checklist

Run sequentially. Each line must be green before claiming M5.6 done.

- [ ] `go test ./...` — full suite green
- [ ] `go build ./...` — wiring compiles
- [ ] `make lint-go` — golangci-lint clean
- [ ] `make lint-compose` — compose config valid
- [ ] `OPS_ENV=production LOCAL_FILE_REPO_ENABLED=true go run ./cmd/server` — must panic at boot
- [ ] `make dev-clean && make dev && make m5-6-local-build-e2e` — e2e passes
- [ ] `curl http://localhost:5000/v2/0ops-apps/personal/node-demo/tags/list | jq` — image tags present
- [ ] `docs/adrs/0012-local-file-repo-dev-mode.md` 與 `docs/features/dev-environment/local-file-repo.md` — 無 placeholder
- [ ] `git log --oneline c00e71b..HEAD` — 每個 commit 單一目的，符合 AGENTS.md § Commits

---

## Spec → Task Coverage

| Spec section | Task |
|---|---|
| sub-spec § 4 OPS_ENV + LOCAL_* env gate | Task 1, Task 2, Task 5, Task 8 |
| sub-spec § 5.2 路徑安全 | Task 2 (validateLocalRepoURL) |
| sub-spec § 5.3 metadata 取得 | Task 3 (LocalInspector) |
| sub-spec § 6.2 dispatcher 行為 | Task 6 |
| sub-spec § 6.3 callback HMAC | Task 5 |
| sub-spec § 6.4 RoutingDispatcher | Task 7 |
| sub-spec § 6.5 retry 策略 | Task 6 (callback retry left to client; doc note) |
| sub-spec § 7 image_ref 格式 | Task 6 (payload.ImageRef passthrough) + Task 8 (LOCAL_REGISTRY env) |
| sub-spec § 8 example repo | Task 4 |
| sub-spec § 9 compose / Makefile | Task 8 |
| sub-spec § 10 state machine | Task 6 (no new state) |
| sub-spec § 11 觀測 | (deferred; metric counters can be added in Task 6 follow-up if needed) |
| sub-spec § 12 測試矩陣 | Task 1-7 各自 unit + Task 9 e2e |
| sub-spec § 15 硬性規則 | Task 1 (panic guard), Task 2 (path validation), Task 7 (TOCTOU re-validate) |
| ADR-0012 § 3.1 三項 env gate | Task 1, Task 5 |
| ADR-0012 § 3.2 介面分派 | Task 3, Task 7 |
| ADR-0012 § 3.3 LocalBuildDispatcher 流程 | Task 6 |
| ADR-0012 § 3.4 路徑安全 | Task 2 |
| ADR-0012 § 3.5 image_ref schema | Task 6 + Task 8 |
| ADR-0012 § 4 與 ADR-0005 之關係 | 全程不改 production GHA dispatch；Task 7 wiring 為條件式 |
