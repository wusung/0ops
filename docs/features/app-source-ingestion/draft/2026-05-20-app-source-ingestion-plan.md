# Production File-Source Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 對 production 開放第二類 app source `upload`：CLI 本地路徑 → `POST /v1/uploads` → upload_id → preview/confirm；server 不解析 host filesystem path；GHA workflow 反向 fetch tarball 後走既有 build/push/callback 鏈。

**Architecture:** 對外契約由 `repo_url` 字串升為 `source` sum type（`github | upload`）；server-side 新增 ingest tree（PVC + team 子目錄 + 程式碼路徑校驗三層隔離）；build path 仍走 ADR-0005 GHA 路徑，新增 workflow 變體用 server 簽的 short-lived JWT 從 server API 反向下載 tarball；ADR-0012 dev mode 路徑保留，不與 production upload path 共用 schema。

**Tech Stack:** Go 1.25 / chi router / pgx / podman compose v2 / GHA workflow_dispatch / paketo `pack build` / tar.zst (klauspost/compress/zstd) / HS256 JWT (golang-jwt/jwt/v5)

**Spec source:** [`docs/features/app-source-ingestion/spec.md`](../spec.md)
**ADR:** ADR-0013（本 plan Task 1 產出）supersede ADR-0012 §3.1「production 必拒」條款

---

## Pre-flight 約定

- **執行入口**：所有 dev 驗證走 `make` target（[Memory: dev 驗證走 compose/Makefile]）；新增 target 寫到 `Makefile.tasks`，root `Makefile` include。
- **測試慣例**：純標準 `testing` + `net/http/httptest`，無 testify。assertion 走 `if got != want { t.Fatalf(...) }`。
- **DB 測試**：透過 `internal/server/db/testpg` helper（既存）；新整合測試走 `t.Parallel()` + truncate-per-test。
- **DTO 路徑**：對外 schema 走 `internal/shared/dto/`，CLI 與 server 共用。
- **Error code**：以 stable code + class 走 `apperror.Write`；新錯誤碼定義在 `internal/server/apperror/error.go`。
- **Commit style**：依 AGENTS.md「Commits」單一目的、命令式；feat / fix / docs / test 分開提交。
- **Migration 編號**：next = `00011_app_source_uploads.sql`。
- **Worktree**：本 plan 建議在 worktree 內逐 task 執行（superpowers:using-git-worktrees）。
- **超大任務拆分**：每個 Task 為一個 PR 候選；merge 順序遵循 Task 編號（除非 Task 內標註可平行）。

## Task 索引

| # | Task | 依賴 | PR 主題（建議） |
|---|---|---|---|
| 1 | ADR-0013 撰寫 | — | `docs(adr): add ADR-0013 production file-source ingestion` |
| 2 | DTO `Source` sum type | 1 | `feat(dto): add Source sum type with backward-compat repo_url` |
| 3 | 新增 apperror 錯誤碼 | 2 | `feat(apperror): add source/upload error codes` |
| 4 | `runtime.AssertProductionSafe` 語意調整 | 1 | `feat(runtime): require upload env in production` |
| 5 | `uploads` 表 migration + queries | 2 | `feat(db): add uploads table + repository methods` |
| 6 | `ingestion.Store` 檔案層 | 5 | `feat(ingestion): add Store with path-safe ingest tree` |
| 7 | `ingestion.Token` short-lived JWT | 5 | `feat(ingestion): add download token signer + verifier` |
| 8 | `POST /v1/uploads` handler | 5,6 | `feat(api): add POST /v1/uploads endpoint` |
| 9 | `GET /v1/uploads/{id}/archive` handler | 6,7 | `feat(api): add JWT-protected upload archive download` |
| 10 | `UploadInspector` + `Source` factory | 6 | `feat(createapp): add UploadInspector + Source factory` |
| 11 | `validateAppCreateRequest` 升 Source | 2,3 | `feat(apps): accept source sum type with repo_url alias` |
| 12 | createapp service 接 Source factory | 10,11 | `feat(createapp): dispatch Source factory in preview` |
| 13 | Confirm 階段 upload pin | 5,12 | `feat(createapp): pin upload on confirm` |
| 14 | `RoutingDispatcher` 加 upload kind | 12,13 | `feat(createapp): route upload source to GHA workflow variant` |
| 15 | `deploy-app-from-upload.yml` workflow | 7,9,14 | `feat(workflows): add deploy-app-from-upload.yml` |
| 16 | CLI tarball packer | 2 | `feat(cli): add local source tarball packer` |
| 17 | CLI multipart upload client | 16 | `feat(cli): add uploads client` |
| 18 | CLI `apps create --source` | 17 | `feat(cli): wire --source flag with bare-path dispatch` |
| 19 | Upload GC reconciler | 5,6 | `feat(reconciler): add upload GC loop` |
| 20 | 配額與 rate-limit 整合 | 8 | `feat(quota): enforce upload size & count quotas` |
| 21 | Metrics + audit | 8,12,14 | `feat(obs): add upload metrics + audit events` |
| 22 | e2e 透過 compose + Makefile | 15,18,19 | `feat(tasks): add app-source-ingestion e2e` |
| 23 | docs sync + CLI deprecation warning | 18 | `docs(app-source-ingestion): finalize feature spec + CLI warning` |

---

### Task 1: ADR-0013 撰寫

**Files:**
- Create: `docs/adrs/0013-production-file-source-ingestion.md`
- Modify: `docs/adr-reading-strategy.md`（§2 快速參考表加 0013 列）

- [ ] **Step 1: 起 ADR 草稿，採 MADR 9-section 結構**

寫入 `docs/adrs/0013-production-file-source-ingestion.md`，frontmatter：

```yaml
---
adr: "0013"
title: Production File-Source Ingestion
status: Accepted
date: 2026-05-20
tags:
  - production
  - app-source
  - upload
  - ingestion
supersedes:
  - "0012"     # §3.1「production 必拒」條款
superseded-by: []
---
```

內容 9 section 對應：
1. Context — ADR-0012 dev only 結論已不足以支撐「不依賴 GitHub 之 production 部署」訴求
2. Decision Drivers — 沿用 ADR-0012 DD2/DD3/DD5/DD7；新增 DD8「server 不解析 host path」、DD9「對外 schema 可演進」
3. Decision Outcome — `source` sum type、upload-based ingest、ingest tree 路徑安全三層、GHA workflow 變體 + short-lived JWT、ADR-0012 dev path 保留
4. 與 ADR-0005 / ADR-0012 之關係 — ADR-0005 GHA 不動；ADR-0012 §3.1 supersede，其餘條款對 dev path 仍有效
5. Pros/Cons — 對照 spec § 1 alternative A1/A2/A3
6. Consequences — 加 PVC 維運面、tarball 大小成本、CLI 體感變化、deprecation timeline
7. Revisit Triggers — OCI artifact registry / self-hosted runner 網路拓撲 / GitHub 依賴完整移除
8. More Information — link spec、ADR-0002/0005/0012、相關 sub-spec
9. Open Questions — 與 spec § 16 對齊

- [ ] **Step 2: 更新 `docs/adr-reading-strategy.md` §2 快速參考表**

在表格末加一列：

```markdown
| **0013** | Production File-Source Ingestion | `source` sum type；server 不解析 host path；upload + GHA workflow 變體 | production 必經 authenticated upload；ADR-0012 §3.1 已 supersede；CLI `--source` 是對外契約 | OCI artifact registry / 全面砍 GHA 依賴 |
```

- [ ] **Step 3: 提交**

```bash
git add docs/adrs/0013-production-file-source-ingestion.md docs/adr-reading-strategy.md
git commit -m "docs(adr): add ADR-0013 production file-source ingestion"
```

---

### Task 2: DTO `Source` sum type

**Files:**
- Create: `internal/shared/dto/source.go`
- Create: `internal/shared/dto/source_test.go`
- Modify: `internal/shared/dto/apps.go`（`AppCreateRequest` 加 `Source`；保留 `RepoURL`/`Ref` deprecated）

- [ ] **Step 1: 寫 failing test `TestSource_MarshalRoundtrip`**

`internal/shared/dto/source_test.go`：

```go
package dto

import (
	"encoding/json"
	"testing"
)

func TestSource_MarshalRoundtrip_GitHub(t *testing.T) {
	in := Source{Type: SourceKindGitHub, GitHub: &SourceGitHub{URL: "https://github.com/foo/bar", Ref: "main"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Source
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != SourceKindGitHub || out.GitHub == nil || out.GitHub.URL != "https://github.com/foo/bar" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
	if out.Upload != nil {
		t.Fatalf("unexpected Upload set on github source")
	}
}

func TestSource_MarshalRoundtrip_Upload(t *testing.T) {
	in := Source{Type: SourceKindUpload, Upload: &SourceUpload{UploadID: "upl_test", Ref: "main"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Source
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != SourceKindUpload || out.Upload == nil || out.Upload.UploadID != "upl_test" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}
```

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/shared/dto/ -run TestSource_MarshalRoundtrip -v
```

Expected: FAIL（type undefined）

- [ ] **Step 3: 實作 `internal/shared/dto/source.go`**

```go
package dto

type SourceKind string

const (
	SourceKindGitHub SourceKind = "github"
	SourceKindUpload SourceKind = "upload"
)

type Source struct {
	Type   SourceKind     `json:"type"`
	GitHub *SourceGitHub  `json:"github,omitempty"`
	Upload *SourceUpload  `json:"upload,omitempty"`
}

type SourceGitHub struct {
	URL string `json:"url"`
	Ref string `json:"ref"`
}

type SourceUpload struct {
	UploadID string `json:"upload_id"`
	Ref      string `json:"ref,omitempty"`
}
```

- [ ] **Step 4: 改 `internal/shared/dto/apps.go` `AppCreateRequest`**

加 `Source *Source` 欄位；保留既有 `RepoURL` / `Ref`（標 deprecated 註解）。維持 JSON omitempty 以保 client 向後相容。

- [ ] **Step 5: 跑 test，預期通過**

```bash
go test ./internal/shared/dto/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/shared/dto/source.go internal/shared/dto/source_test.go internal/shared/dto/apps.go
git commit -m "feat(dto): add Source sum type with backward-compat repo_url"
```

---

### Task 3: 新增 apperror 錯誤碼

**Files:**
- Modify: `internal/server/apperror/error.go`
- Modify: `internal/server/apperror/error_test.go`（若有；若無則新建）

- [ ] **Step 1: 找出既有錯誤碼定義位置**

```bash
rg -n "ClassUnprocessable|source_required|unsupported_repo_url" internal/server/apperror/ internal/server/apps.go
```

- [ ] **Step 2: 在 `internal/server/apperror/error.go` 新增穩定錯誤碼常數**

```go
// Source / upload validation (M6 app-source-ingestion)
const (
	CodeSourceRequired           = "source_required"
	CodeSourceConflict           = "source_conflict"
	CodeSourceInvalid            = "source_invalid"
	CodeSourceKindUnsupported    = "source_kind_unsupported"
	CodeUnsupportedSource        = "unsupported_source"
	CodePayloadTooLarge          = "payload_too_large"
	CodeUnsupportedArchive       = "unsupported_archive_format"
	CodeArchiveCorrupt           = "archive_corrupt"
	CodeSHA256Mismatch           = "sha256_mismatch"
	CodeUploadRateLimited        = "upload_rate_limited"
	CodeTeamQuotaExceeded        = "team_quota_exceeded"
	CodeSourceNotFound           = "source_not_found"
	CodeSourceExpired            = "source_expired"
	CodeUploadCrossTeam          = "upload_cross_team"
)
```

- [ ] **Step 3: 寫 test 驗錯誤碼穩定（不被 typo 改動）**

`internal/server/apperror/error_test.go`：

```go
func TestSourceCodesStable(t *testing.T) {
	tests := map[string]string{
		"source_required":           CodeSourceRequired,
		"source_conflict":           CodeSourceConflict,
		"source_invalid":            CodeSourceInvalid,
		"source_kind_unsupported":   CodeSourceKindUnsupported,
		"unsupported_source":        CodeUnsupportedSource,
		"payload_too_large":         CodePayloadTooLarge,
		"unsupported_archive_format": CodeUnsupportedArchive,
		"archive_corrupt":           CodeArchiveCorrupt,
		"sha256_mismatch":           CodeSHA256Mismatch,
		"upload_rate_limited":       CodeUploadRateLimited,
		"team_quota_exceeded":       CodeTeamQuotaExceeded,
		"source_not_found":          CodeSourceNotFound,
		"source_expired":            CodeSourceExpired,
		"upload_cross_team":         CodeUploadCrossTeam,
	}
	for want, got := range tests {
		if got != want {
			t.Errorf("code mismatch: got %q, want %q", got, want)
		}
	}
}
```

- [ ] **Step 4: 跑 test**

```bash
go test ./internal/server/apperror/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/apperror/
git commit -m "feat(apperror): add source/upload stable error codes"
```

---

### Task 4: `runtime.AssertProductionSafe` 語意調整

**Files:**
- Modify: `internal/shared/runtime/production_safety.go`（既有）
- Modify: `internal/shared/runtime/production_safety_test.go`（既有）

- [ ] **Step 1: 讀現況**

```bash
cat internal/shared/runtime/production_safety.go
```

確認既有 panic 條件：`OPS_ENV=production` 且任一 `LOCAL_*_ENABLED=true`。

- [ ] **Step 2: 寫 failing test：production 缺 APP_SOURCE_INGEST_ROOT → panic**

`production_safety_test.go` 加：

```go
func TestAssertProductionSafe_PanicsOnMissingIngestRoot(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "x")
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for missing APP_SOURCE_INGEST_ROOT")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafe_PanicsOnMissingBuildTokenSecret(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "/var/lib/0ops/uploads")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "")
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for missing OPS_BUILD_TOKEN_SECRET")
		}
	}()
	AssertProductionSafe()
}

func TestAssertProductionSafe_PassesWhenProductionWithUploadEnv(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	t.Setenv("APP_SOURCE_INGEST_ROOT", "/var/lib/0ops/uploads")
	t.Setenv("OPS_BUILD_TOKEN_SECRET", "deadbeef")
	t.Setenv("LOCAL_FILE_REPO_ENABLED", "")
	t.Setenv("LOCAL_BUILD_ENABLED", "")
	t.Setenv("LOCAL_REGISTRY", "")
	AssertProductionSafe()
}
```

- [ ] **Step 3: 跑 test，預期失敗**

```bash
go test ./internal/shared/runtime/ -run TestAssertProductionSafe -v
```

Expected: FAIL（assertion 未檢查新 env）

- [ ] **Step 4: 改 `production_safety.go`**

新增檢查：

```go
// AssertProductionSafe enforces the production invariants:
//  - LOCAL_FILE_REPO_ENABLED / LOCAL_BUILD_ENABLED / LOCAL_REGISTRY must NOT be set in production (ADR-0012)
//  - APP_SOURCE_INGEST_ROOT and OPS_BUILD_TOKEN_SECRET MUST be set in production (ADR-0013)
func AssertProductionSafe() {
	if os.Getenv("OPS_ENV") != "production" {
		return
	}
	for _, k := range []string{"LOCAL_FILE_REPO_ENABLED", "LOCAL_BUILD_ENABLED"} {
		if strings.EqualFold(os.Getenv(k), "true") {
			panic("ADR-0012: " + k + " must be unset in production")
		}
	}
	if os.Getenv("LOCAL_REGISTRY") != "" {
		panic("ADR-0012: LOCAL_REGISTRY must be empty in production")
	}
	if os.Getenv("APP_SOURCE_INGEST_ROOT") == "" {
		panic("ADR-0013: APP_SOURCE_INGEST_ROOT must be set in production")
	}
	if os.Getenv("OPS_BUILD_TOKEN_SECRET") == "" {
		panic("ADR-0013: OPS_BUILD_TOKEN_SECRET must be set in production")
	}
}
```

- [ ] **Step 5: 跑全部 production_safety test**

```bash
go test ./internal/shared/runtime/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/shared/runtime/
git commit -m "feat(runtime): require APP_SOURCE_INGEST_ROOT and OPS_BUILD_TOKEN_SECRET in production"
```

---

### Task 5: `uploads` 表 migration + repository

**Files:**
- Create: `migrations/00011_app_source_uploads.sql`
- Modify: `internal/server/db/queries.sql`（加 query 名稱常量）
- Create: `internal/server/db/uploads.go`
- Create: `internal/server/db/uploads_test.go`

- [ ] **Step 1: 寫 migration**

`migrations/00011_app_source_uploads.sql`：

```sql
-- +migrate Up
CREATE TABLE app_source_uploads (
    id             TEXT PRIMARY KEY,           -- upl_<ulid>
    team_id        TEXT NOT NULL,
    actor_user_id  TEXT NOT NULL,
    size_bytes     BIGINT NOT NULL,
    sha256         TEXT NOT NULL,
    archive_format TEXT NOT NULL,              -- 'tar.zst' | 'tar.gz'
    status         TEXT NOT NULL DEFAULT 'received',  -- received | pinned | expired | gc'd
    pinned_at      TIMESTAMPTZ,
    expires_at     TIMESTAMPTZ NOT NULL,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    gc_at          TIMESTAMPTZ,
    CONSTRAINT app_source_uploads_status_chk
        CHECK (status IN ('received','pinned','expired','gc''d'))
);

CREATE INDEX idx_app_source_uploads_team_status
    ON app_source_uploads (team_id, status);

CREATE INDEX idx_app_source_uploads_expires
    ON app_source_uploads (expires_at)
    WHERE status IN ('received','pinned');

-- +migrate Down
DROP TABLE app_source_uploads;
```

- [ ] **Step 2: 跑 migration 確認**

```bash
make migrate
```

Expected: 00011 ran without error.

- [ ] **Step 3: 寫 failing test `internal/server/db/uploads_test.go`**

```go
func TestUploadRepository_InsertAndGet(t *testing.T) {
	pg := testpg.New(t)
	repo := NewRepository(pg.Pool)
	ctx := context.Background()

	row := Upload{
		ID: "upl_test1", TeamID: "team_a", ActorUserID: "u_1",
		SizeBytes: 1024, SHA256: "deadbeef", ArchiveFormat: "tar.zst",
		Status: "received", ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
	if err := repo.InsertUpload(ctx, row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := repo.GetUpload(ctx, "team_a", "upl_test1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SHA256 != "deadbeef" {
		t.Fatalf("sha mismatch: %s", got.SHA256)
	}
}

func TestUploadRepository_GetCrossTeamReturnsNotFound(t *testing.T) {
	pg := testpg.New(t)
	repo := NewRepository(pg.Pool)
	ctx := context.Background()
	_ = repo.InsertUpload(ctx, Upload{ID: "upl_x", TeamID: "team_a", ActorUserID: "u_1",
		SizeBytes: 1, SHA256: "x", ArchiveFormat: "tar.zst", Status: "received",
		ExpiresAt: time.Now().UTC().Add(time.Hour)})
	_, err := repo.GetUpload(ctx, "team_b", "upl_x")
	if !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("expected ErrUploadNotFound, got %v", err)
	}
}

func TestUploadRepository_PinAndListExpired(t *testing.T) {
	pg := testpg.New(t)
	repo := NewRepository(pg.Pool)
	ctx := context.Background()
	_ = repo.InsertUpload(ctx, Upload{ID: "upl_p", TeamID: "team_a", ActorUserID: "u_1",
		SizeBytes: 1, SHA256: "x", ArchiveFormat: "tar.zst", Status: "received",
		ExpiresAt: time.Now().UTC().Add(-time.Hour)})
	expired, err := repo.ListExpiredUploads(ctx, 10)
	if err != nil || len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %v err=%v", expired, err)
	}
}
```

- [ ] **Step 4: 跑 test，預期失敗**

```bash
go test ./internal/server/db/ -run TestUploadRepository -v
```

Expected: FAIL（type/method undefined）

- [ ] **Step 5: 實作 `internal/server/db/uploads.go`**

```go
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Upload struct {
	ID            string
	TeamID        string
	ActorUserID   string
	SizeBytes     int64
	SHA256        string
	ArchiveFormat string
	Status        string
	PinnedAt      *time.Time
	ExpiresAt     time.Time
	ReceivedAt    time.Time
}

var ErrUploadNotFound = errors.New("upload not found")

func (r *Repository) InsertUpload(ctx context.Context, u Upload) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO app_source_uploads
			(id, team_id, actor_user_id, size_bytes, sha256, archive_format, status, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		u.ID, u.TeamID, u.ActorUserID, u.SizeBytes, u.SHA256, u.ArchiveFormat, u.Status, u.ExpiresAt)
	return err
}

func (r *Repository) GetUpload(ctx context.Context, teamID, id string) (Upload, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, team_id, actor_user_id, size_bytes, sha256, archive_format,
		       status, pinned_at, expires_at, received_at
		FROM app_source_uploads
		WHERE team_id = $1 AND id = $2`, teamID, id)
	var u Upload
	if err := row.Scan(&u.ID, &u.TeamID, &u.ActorUserID, &u.SizeBytes, &u.SHA256,
		&u.ArchiveFormat, &u.Status, &u.PinnedAt, &u.ExpiresAt, &u.ReceivedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Upload{}, ErrUploadNotFound
		}
		return Upload{}, err
	}
	return u, nil
}

func (r *Repository) PinUpload(ctx context.Context, teamID, id string, expiresAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE app_source_uploads
		   SET status = 'pinned', pinned_at = NOW(), expires_at = $3
		 WHERE team_id = $1 AND id = $2 AND status = 'received'`, teamID, id, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUploadNotFound
	}
	return nil
}

func (r *Repository) ListExpiredUploads(ctx context.Context, limit int) ([]Upload, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, team_id, status
		FROM app_source_uploads
		WHERE expires_at < NOW() AND status IN ('received','pinned')
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		var u Upload
		if err := rows.Scan(&u.ID, &u.TeamID, &u.Status); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repository) MarkUploadGCd(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE app_source_uploads
		   SET status = 'gc''d', gc_at = NOW()
		 WHERE id = $1`, id)
	return err
}
```

- [ ] **Step 6: 跑 test，預期通過**

```bash
go test ./internal/server/db/ -run TestUploadRepository -v
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add migrations/00011_app_source_uploads.sql internal/server/db/uploads.go internal/server/db/uploads_test.go
git commit -m "feat(db): add app_source_uploads table + repository"
```

---

### Task 6: `ingestion.Store` 檔案層

**Files:**
- Create: `internal/server/services/createapp/ingestion/store.go`
- Create: `internal/server/services/createapp/ingestion/store_test.go`

- [ ] **Step 1: 寫 failing test `TestStore_PutOpenAndPathSafety`**

```go
package ingestion

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore_PutWritesUnderTeamDir(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root}
	archive := bytes.NewReader(syntheticTarZst(t, map[string]string{"app.js": "console.log('hi')"}))
	got, err := s.Put(context.Background(), "team_a", "upl_x", archive, "tar.zst")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if got.Path != filepath.Join(root, "team_a", "upl_x") {
		t.Fatalf("unexpected path: %s", got.Path)
	}
	if _, err := os.Stat(filepath.Join(got.Path, "tree", "app.js")); err != nil {
		t.Fatalf("tree file missing: %v", err)
	}
}

func TestStore_OpenRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root}
	if _, err := s.Open(context.Background(), "team_a", "upl_x", "../etc/passwd"); err == nil ||
		!strings.Contains(err.Error(), "path escape") {
		t.Fatalf("expected path escape err, got %v", err)
	}
}

func TestStore_PutRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	s := &Store{Root: root}
	archive := syntheticTarZstWithSymlink(t, "evil", "../../etc/passwd")
	if _, err := s.Put(context.Background(), "team_a", "upl_x", bytes.NewReader(archive), "tar.zst"); err == nil {
		t.Fatalf("expected symlink escape rejection")
	}
}
```

需新增測試 helper `syntheticTarZst` / `syntheticTarZstWithSymlink`，列於同檔尾。

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/server/services/createapp/ingestion/ -v
```

Expected: FAIL（package undefined）

- [ ] **Step 3: 實作 `store.go`**

要點：
- `Store struct { Root string; MaxArchiveBytes int64; MaxEntryBytes int64; MaxEntries int }`
- `Put(ctx, teamID, uploadID, r io.Reader, format string) (StoredUpload, error)`：
  - 解 magic byte 確認 format
  - 先寫 `_archive.tar.<fmt>`（原檔保留）
  - 解壓到 `tree/`；每 entry 做 `filepath.Clean` + `..` reject + size cap + entry count cap
  - symlink target 走 `filepath.Clean`；rel-path 校驗必須在 `<teamDir>/<uploadID>/tree/` 內
  - mode mask 0644 / 0755
  - 寫入 `_meta.json`（sha256、size、received_at）
- `Open(ctx, teamID, uploadID, relPath string) (io.ReadCloser, error)`：
  - `joined = filepath.Join(Root, teamID, uploadID, "tree", filepath.Clean("/"+relPath))`
  - `rel, _ := filepath.Rel(filepath.Join(Root, teamID, uploadID, "tree"), joined)`
  - reject if `strings.HasPrefix(rel, "..")`
- `Archive(ctx, teamID, uploadID string) (io.ReadCloser, error)`：開原檔 `_archive.tar.<fmt>`
- `Delete(ctx, teamID, uploadID string) error`：rename 到 `_trash/<id>` 再下次掃刪
- `RootForTeam(teamID string) string` helper

依賴：`github.com/klauspost/compress/zstd`（已在 go.mod 或新增）。

- [ ] **Step 4: 跑 test，預期通過**

```bash
go test ./internal/server/services/createapp/ingestion/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/services/createapp/ingestion/
git commit -m "feat(ingestion): add Store with path-safe ingest tree"
```

---

### Task 7: `ingestion.Token` short-lived JWT

**Files:**
- Create: `internal/server/services/createapp/ingestion/token.go`
- Create: `internal/server/services/createapp/ingestion/token_test.go`

- [ ] **Step 1: 寫 failing test**

```go
func TestSignAndVerify_Roundtrip(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("test-secret"), TTL: 15 * time.Minute}
	tok, err := signer.Sign(TokenClaims{TeamID: "team_a", UploadID: "upl_x", DeployRunID: "run_1"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.TeamID != "team_a" || claims.UploadID != "upl_x" || claims.DeployRunID != "run_1" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

func TestVerify_RejectsExpired(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("s"), TTL: -time.Minute}
	tok, _ := signer.Sign(TokenClaims{TeamID: "team_a", UploadID: "u", DeployRunID: "r"})
	if _, err := signer.Verify(tok); err == nil {
		t.Fatalf("expected expired error")
	}
}

func TestVerify_RejectsWrongSecret(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("a"), TTL: time.Hour}
	tok, _ := signer.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r"})
	other := &TokenSigner{Secret: []byte("b"), TTL: time.Hour}
	if _, err := other.Verify(tok); err == nil {
		t.Fatalf("expected sig mismatch error")
	}
}

func TestVerify_RejectsWrongScope(t *testing.T) {
	signer := &TokenSigner{Secret: []byte("s"), TTL: time.Hour}
	tok, _ := signer.Sign(TokenClaims{TeamID: "t", UploadID: "u", DeployRunID: "r", Scope: "not-download"})
	if _, err := signer.Verify(tok); err == nil {
		t.Fatalf("expected scope rejection")
	}
}
```

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/server/services/createapp/ingestion/ -run TestSign -v
```

Expected: FAIL

- [ ] **Step 3: 實作 `token.go`**

採 `github.com/golang-jwt/jwt/v5`（若 repo 已用過則同源；否則新增依賴）。HS256，scope 預設並強制 `download-upload`。Audience `gha-build`。

```go
type TokenClaims struct {
	TeamID       string
	UploadID     string
	DeployRunID  string
	Scope        string // forced to "download-upload"
}

type TokenSigner struct {
	Secret []byte
	TTL    time.Duration
}

func (s *TokenSigner) Sign(c TokenClaims) (string, error) { /* ... */ }
func (s *TokenSigner) Verify(token string) (TokenClaims, error) { /* validate exp + scope + aud */ }
```

- [ ] **Step 4: 跑 test，預期通過**

```bash
go test ./internal/server/services/createapp/ingestion/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/services/createapp/ingestion/token.go internal/server/services/createapp/ingestion/token_test.go
git commit -m "feat(ingestion): add short-lived JWT signer and verifier"
```

---

### Task 8: `POST /v1/uploads` handler

**Files:**
- Create: `internal/server/uploads.go`
- Create: `internal/server/uploads_test.go`
- Modify: `internal/server/apps.go`（router 註冊新 endpoint；存放在 apps.go 的 NewRouterWith* 內）

- [ ] **Step 1: 寫 failing test `TestPostUploads_HappyPath`**

```go
func TestPostUploads_HappyPath(t *testing.T) {
	pg := testpg.New(t)
	root := t.TempDir()
	t.Setenv("APP_SOURCE_INGEST_ROOT", root)

	repo := db.NewRepository(pg.Pool)
	r := newTestRouter(t, repo)

	body, contentType := multipartBody(t, "archive", "demo.tar.zst", syntheticTarZst(t, map[string]string{"package.json": `{"name":"demo"}`}))
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads", body)
	req.Header.Set("Content-Type", contentType)
	authHeader(req, "team_a")  // helper sets bearer + ctx

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct{ UploadID string `json:"upload_id"` }
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !strings.HasPrefix(resp.UploadID, "upl_") {
		t.Fatalf("bad upload_id: %s", resp.UploadID)
	}
}

func TestPostUploads_RejectsOversize(t *testing.T) { /* 設 cap=1KB；送 2KB → 413 */ }
func TestPostUploads_RejectsBadArchive(t *testing.T) { /* 送非 tar 內容 → 422 */ }
func TestPostUploads_RequiresAuth(t *testing.T) { /* 無 bearer → 401 */ }
func TestPostUploads_RejectsCrossTeamID(t *testing.T) { /* team_id 與 token claim 不符 → 403 */ }
```

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/server/ -run TestPostUploads -v
```

Expected: FAIL（handler undefined）

- [ ] **Step 3: 實作 handler `internal/server/uploads.go`**

要點：
- 路由：`r.Post("/v1/uploads", uploadHandler(store, ingestionStore))`
- multipart parse with `MaxMemory = 1MB`；archive 走 `r.MultipartReader()` stream 不落地全載入記憶體
- 強制 size cap：`io.LimitReader(part, MaxBytes+1)`；超過 → 413
- SHA256 邊讀邊算；與 client 提供之 `sha256` form field 比對
- ULID 生成 upload_id（既有：`internal/server/util.NewULID` 或類似；若無則新增）
- 呼叫 `ingestionStore.Put(ctx, teamID, uploadID, archive, format)` 寫檔
- 成功 → `repo.InsertUpload(...)` + audit `app_source.upload.created` + 201

- [ ] **Step 4: 在 `apps.go` 之 NewRouterWith* 加 mount 點**

找到 `r.Post("/v1/admin/bootstrap-owner", ...)` 同階層，加：

```go
r.Post("/v1/uploads", uploadHandler(store, ingestionStore))
```

並把 `ingestionStore` 從 `cmd/server/main.go` 透過 router constructor 傳入（router signature 加 `ingestionStore *ingestion.Store`）。

- [ ] **Step 5: 跑 test，預期通過**

```bash
go test ./internal/server/ -run TestPostUploads -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/server/uploads.go internal/server/uploads_test.go internal/server/apps.go cmd/server/main.go
git commit -m "feat(api): add POST /v1/uploads endpoint"
```

---

### Task 9: `GET /v1/uploads/{id}/archive` handler

**Files:**
- Modify: `internal/server/uploads.go`
- Modify: `internal/server/uploads_test.go`
- Modify: `internal/server/apps.go`（router）

- [ ] **Step 1: 寫 failing test**

```go
func TestGetUploadArchive_HappyPath_WithJWT(t *testing.T) {
	// upload 已存在；用 TokenSigner 簽 token；GET 帶 Authorization: Bearer <token>
	// 預期 200 + tar.zst content + correct Content-Type
}

func TestGetUploadArchive_RejectsExpiredToken(t *testing.T) { /* 401 */ }
func TestGetUploadArchive_RejectsWrongUploadID(t *testing.T) { /* token claim != path → 403 */ }
func TestGetUploadArchive_RejectsCrossTeam(t *testing.T) { /* token claim team_id != upload.team_id → 403 */ }
```

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/server/ -run TestGetUploadArchive -v
```

Expected: FAIL

- [ ] **Step 3: 實作 handler**

```go
func uploadArchiveHandler(store *db.Repository, ing *ingestion.Store, signer *ingestion.TokenSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims, err := signer.Verify(token)
		if err != nil { apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "invalid token", nil); return }

		uploadID := chi.URLParam(r, "id")
		if claims.UploadID != uploadID {
			apperror.Write(w, "forbidden", apperror.ClassForbidden, "upload id mismatch", nil)
			return
		}
		up, err := store.GetUpload(r.Context(), claims.TeamID, uploadID)
		if err != nil { apperror.Write(w, apperror.CodeSourceNotFound, apperror.ClassNotFound, "upload not found", nil); return }
		rc, err := ing.Archive(r.Context(), up.TeamID, up.ID)
		if err != nil { apperror.Write(w, "internal_error", apperror.ClassInternal, "archive read failed", nil); return }
		defer rc.Close()
		w.Header().Set("Content-Type", "application/zstd")
		_, _ = io.Copy(w, rc)
	}
}
```

加 router 註冊：

```go
r.Get("/v1/uploads/{id}/archive", uploadArchiveHandler(store, ingestionStore, buildTokenSigner))
```

- [ ] **Step 4: 跑 test，預期通過**

```bash
go test ./internal/server/ -run TestGetUploadArchive -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/uploads.go internal/server/uploads_test.go internal/server/apps.go cmd/server/main.go
git commit -m "feat(api): add JWT-protected GET /v1/uploads/{id}/archive"
```

---

### Task 10: `UploadInspector` + `Source` factory

**Files:**
- Create: `internal/server/services/createapp/upload_inspect.go`
- Create: `internal/server/services/createapp/upload_inspect_test.go`
- Create: `internal/server/services/createapp/source.go`
- Create: `internal/server/services/createapp/source_test.go`

- [ ] **Step 1: 寫 failing test `TestUploadInspector_DetectsPaketoFramework`**

```go
func TestUploadInspector_NodeProject(t *testing.T) {
	root := t.TempDir()
	store := &ingestion.Store{Root: root}
	_, _ = store.Put(context.Background(), "team_a", "upl_x",
		bytes.NewReader(syntheticTarZst(t, map[string]string{"package.json": `{"name":"demo"}`})), "tar.zst")

	insp := &UploadInspector{Store: store, Repo: nil}
	meta, err := insp.Inspect(context.Background(), Source{
		Type: dto.SourceKindUpload,
		Upload: &dto.SourceUpload{UploadID: "upl_x"},
	}, "team_a")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if meta.Framework != "node" {
		t.Fatalf("framework: %s", meta.Framework)
	}
}

func TestSourceFactory_DispatchesByType(t *testing.T) {
	gh := &fakeInspector{name: "gh"}
	up := &fakeInspector{name: "up"}
	f := SourceFactory{GitHub: gh, Upload: up}

	got, _ := f.For(Source{Type: dto.SourceKindUpload, Upload: &dto.SourceUpload{UploadID: "u"}})
	if got != up { t.Fatalf("expected upload inspector") }
}
```

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/server/services/createapp/ -run TestUploadInspector -v
```

Expected: FAIL

- [ ] **Step 3: 實作 `upload_inspect.go` 與 `source.go`**

`source.go`：

```go
type Source = dto.Source

type Inspector interface {
	Inspect(ctx context.Context, src Source, teamID string) (RepoMetadata, error)
}

type SourceFactory struct {
	GitHub Inspector
	Upload Inspector
}

func (f SourceFactory) For(src Source) (Inspector, error) {
	switch src.Type {
	case dto.SourceKindGitHub: return f.GitHub, nil
	case dto.SourceKindUpload: return f.Upload, nil
	default: return nil, fmt.Errorf("unknown source kind: %s", src.Type)
	}
}
```

`upload_inspect.go`：

```go
type UploadInspector struct {
	Store *ingestion.Store
	Repo  uploadStore // interface with GetUpload
}

func (u *UploadInspector) Inspect(ctx context.Context, src Source, teamID string) (RepoMetadata, error) {
	if src.Type != dto.SourceKindUpload || src.Upload == nil { return RepoMetadata{}, ErrInvalidSource }
	row, err := u.Repo.GetUpload(ctx, teamID, src.Upload.UploadID)
	if err != nil { return RepoMetadata{}, err }
	rc, err := u.Store.Open(ctx, teamID, row.ID, "package.json")
	// detect framework based on files in tree; reuse existing detectFramework helper
}
```

- [ ] **Step 4: 跑 test，預期通過**

```bash
go test ./internal/server/services/createapp/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/services/createapp/upload_inspect.go internal/server/services/createapp/upload_inspect_test.go internal/server/services/createapp/source.go internal/server/services/createapp/source_test.go
git commit -m "feat(createapp): add UploadInspector + Source factory"
```

---

### Task 11: `validateAppCreateRequest` 升 Source schema + repo_url alias

**Files:**
- Modify: `internal/server/apps.go:1762`（`validateAppCreateRequest`）
- Modify: `internal/server/apps_test.go`

- [ ] **Step 1: 讀現況**

```bash
sed -n '1762,1810p' internal/server/apps.go
```

- [ ] **Step 2: 寫 failing test**

```go
func TestValidateAppCreate_AcceptsSourceUpload(t *testing.T) {
	req := dto.AppCreateRequest{Slug: "demo", Source: &dto.Source{Type: dto.SourceKindUpload, Upload: &dto.SourceUpload{UploadID: "upl_x"}}}
	rr := httptest.NewRecorder()
	if !validateAppCreateRequest(rr, req) {
		t.Fatalf("expected accept, body=%s", rr.Body.String())
	}
}

func TestValidateAppCreate_AcceptsSourceGitHub(t *testing.T) { /* type=github */ }

func TestValidateAppCreate_NormalizesRepoURLToGitHubSource(t *testing.T) {
	req := dto.AppCreateRequest{Slug: "demo", RepoURL: "https://github.com/foo/bar", Ref: "main"}
	rr := httptest.NewRecorder()
	if !validateAppCreateRequest(rr, req) {
		t.Fatalf("expected accept")
	}
	// after validation, req.Source must be populated
}

func TestValidateAppCreate_RejectsSourceAndRepoURLTogether(t *testing.T) {
	req := dto.AppCreateRequest{Slug: "demo",
		Source: &dto.Source{Type: dto.SourceKindUpload, Upload: &dto.SourceUpload{UploadID: "u"}},
		RepoURL: "https://github.com/x/y"}
	rr := httptest.NewRecorder()
	if validateAppCreateRequest(rr, req) {
		t.Fatalf("expected reject")
	}
	if !strings.Contains(rr.Body.String(), "source_conflict") {
		t.Fatalf("expected source_conflict, body=%s", rr.Body.String())
	}
}

func TestValidateAppCreate_RejectsFileURLInProduction(t *testing.T) {
	t.Setenv("OPS_ENV", "production")
	req := dto.AppCreateRequest{Slug: "demo", RepoURL: "file:///workspace/x"}
	rr := httptest.NewRecorder()
	if validateAppCreateRequest(rr, req) {
		t.Fatalf("expected reject")
	}
}
```

- [ ] **Step 3: 跑 test，預期失敗**

```bash
go test ./internal/server/ -run TestValidateAppCreate -v
```

Expected: FAIL

- [ ] **Step 4: 改 `validateAppCreateRequest` 為 pointer receiver，並 normalize repo_url → Source**

新版邏輯：

```go
func validateAppCreateRequest(w http.ResponseWriter, req *dto.AppCreateRequest) bool {
	// slug 不變

	// 1. 互斥檢查
	if req.Source != nil && strings.TrimSpace(req.RepoURL) != "" {
		apperror.Write(w, apperror.CodeSourceConflict, apperror.ClassUnprocessable, "use source or repo_url, not both", nil)
		return false
	}

	// 2. RepoURL → Source.GitHub normalize（backward compat）
	if req.Source == nil && strings.TrimSpace(req.RepoURL) != "" {
		repoURL := strings.TrimSpace(req.RepoURL)
		switch {
		case strings.HasPrefix(repoURL, "file://"):
			// dev path 走 ADR-0012；production 拒絕
			if os.Getenv("OPS_ENV") == "production" {
				apperror.Write(w, apperror.CodeUnsupportedSource, apperror.ClassUnprocessable, "file:// is not supported in production; use --source <path> to upload", map[string]any{"field": "repo_url"})
				return false
			}
			// 既有 ADR-0012 dev path 邏輯保留：走 ValidateLocalRepoURL ...
			// (此處保留原 case 邏輯，僅外層判 production)
			if err := createappsvc.ValidateLocalRepoURL(repoURL); err != nil {
				// 既有錯誤映射
			}
			req.Source = &dto.Source{Type: dto.SourceKindGitHub /* dev path 仍走 github 介面到 LocalInspector? 不；保留 sentinel */ }
			// 為避免混淆 dev path schema，dev mode 不 normalize 至 Source；後續 service 仍接 req.RepoURL
		case strings.HasPrefix(repoURL, "https://github.com/"), strings.HasPrefix(repoURL, "git@github.com:"):
			req.Source = &dto.Source{Type: dto.SourceKindGitHub, GitHub: &dto.SourceGitHub{URL: repoURL, Ref: strings.TrimSpace(req.Ref)}}
		default:
			apperror.Write(w, apperror.CodeUnsupportedSource, apperror.ClassUnprocessable, "unsupported repo_url", map[string]any{"field": "repo_url"})
			return false
		}
	}

	// 3. Source 必填
	if req.Source == nil {
		apperror.Write(w, apperror.CodeSourceRequired, apperror.ClassBadRequest, "source is required", nil)
		return false
	}

	// 4. Source 內欄位 sanity
	switch req.Source.Type {
	case dto.SourceKindGitHub:
		if req.Source.GitHub == nil || req.Source.GitHub.URL == "" || req.Source.GitHub.Ref == "" {
			apperror.Write(w, apperror.CodeSourceInvalid, apperror.ClassUnprocessable, "github source incomplete", nil); return false
		}
	case dto.SourceKindUpload:
		if req.Source.Upload == nil || req.Source.Upload.UploadID == "" {
			apperror.Write(w, apperror.CodeSourceInvalid, apperror.ClassUnprocessable, "upload source incomplete", nil); return false
		}
	default:
		apperror.Write(w, apperror.CodeSourceKindUnsupported, apperror.ClassUnprocessable, "unknown source kind", nil); return false
	}
	return true
}
```

更新 call-site：把 `req` 改為傳指標。

- [ ] **Step 5: 跑 test，預期通過**

```bash
go test ./internal/server/ -run TestValidateAppCreate -v
```

Expected: PASS

- [ ] **Step 6: 跑既有 apps_test 確認不破**

```bash
go test ./internal/server/ -v
```

Expected: PASS（既有 `file://` dev path 測試走 `OPS_ENV` 非 production 路徑仍有效）

- [ ] **Step 7: 提交**

```bash
git add internal/server/apps.go internal/server/apps_test.go
git commit -m "feat(apps): accept source sum type with repo_url alias"
```

---

### Task 12: createapp service 接 Source factory

**Files:**
- Modify: `internal/server/services/createapp/service.go`
- Modify: `internal/server/services/createapp/service_test.go`
- Modify: `internal/server/apps.go`（service 構造處傳入 SourceFactory 與 UploadInspector）
- Modify: `cmd/server/main.go`（boot 注入）

- [ ] **Step 1: 讀現況 service 內 `inspect_repo` 呼叫位置**

```bash
rg -n "inspect_repo|inspector|Inspect\(" internal/server/services/createapp/service.go
```

- [ ] **Step 2: 寫 failing test：upload source 進 preview 階段呼叫 UploadInspector**

```go
func TestPreview_UploadSource_CallsUploadInspector(t *testing.T) {
	rec := &recordingInspector{}
	svc := newServiceWithInspectors(t, &recordingInspector{}, rec)
	_, err := svc.Preview(ctx, dto.AppCreateRequest{
		Slug: "demo",
		Source: &dto.Source{Type: dto.SourceKindUpload, Upload: &dto.SourceUpload{UploadID: "upl_x"}},
	})
	if err != nil { t.Fatalf("preview: %v", err) }
	if rec.calls != 1 { t.Fatalf("expected UploadInspector called, got %d", rec.calls) }
}
```

- [ ] **Step 3: 跑 test，預期失敗**

Expected: FAIL（service 仍走 GitHubInspector unconditionally）

- [ ] **Step 4: 改 service：以 SourceFactory 取代 single Inspector field**

```go
type Service struct {
	store      Store
	factory    SourceFactory
	dispatcher Dispatcher
	// ...
}

func (s *Service) Preview(ctx context.Context, req dto.AppCreateRequest) (PreviewResult, error) {
	inspector, err := s.factory.For(*req.Source)
	if err != nil { return PreviewResult{}, err }
	meta, err := inspector.Inspect(ctx, *req.Source, teamIDFromCtx(ctx))
	// 其餘流程不變
}
```

`apps.go` 與 `main.go` 注入 `SourceFactory{GitHub: githubInspector, Upload: uploadInspector}`。

- [ ] **Step 5: 跑 test**

```bash
go test ./internal/server/services/createapp/ -v
```

Expected: PASS

- [ ] **Step 6: 跑全 server test 確認 GitHub path 不破**

```bash
go test ./internal/server/... -v
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/server/services/createapp/service.go internal/server/services/createapp/service_test.go internal/server/apps.go cmd/server/main.go
git commit -m "feat(createapp): dispatch SourceFactory in preview"
```

---

### Task 13: Confirm 階段 upload pin

**Files:**
- Modify: `internal/server/services/createapp/service.go`（confirm 路徑）
- Modify: `internal/server/services/createapp/service_test.go`

- [ ] **Step 1: 寫 failing test**

```go
func TestConfirm_UploadSource_PinsUpload(t *testing.T) {
	repo := newFakeUploadRepo()
	repo.InsertUpload(ctx, db.Upload{ID: "upl_x", TeamID: "team_a", Status: "received", ExpiresAt: time.Now().Add(time.Hour)})
	svc := newServiceWithUploadRepo(t, repo)

	_, _ = svc.Confirm(ctx, preview.ID, preview.Token)

	got, _ := repo.GetUpload(ctx, "team_a", "upl_x")
	if got.Status != "pinned" {
		t.Fatalf("expected pinned, got %s", got.Status)
	}
}
```

- [ ] **Step 2: 跑 test，預期失敗**

Expected: FAIL

- [ ] **Step 3: 改 service.Confirm**

upload kind 之 source，在 deploy_run insert 成功後呼叫 `repo.PinUpload(ctx, teamID, uploadID, terminal_at+7d)`。任何失敗 → audit + log warning（不阻斷 confirm 主流程；upload 可隨 GC 自然清理）。

- [ ] **Step 4: 跑 test，預期通過**

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/services/createapp/service.go internal/server/services/createapp/service_test.go
git commit -m "feat(createapp): pin upload on confirm"
```

---

### Task 14: `RoutingDispatcher` 加 upload kind

**Files:**
- Modify: `internal/server/services/createapp/routing_dispatcher.go`
- Modify: `internal/server/services/createapp/routing_dispatcher_test.go`
- Create: `internal/server/services/createapp/upload_dispatcher.go`（新 dispatcher：build GHA payload 並呼叫 workflowdispatch.Client）
- Modify: `internal/server/services/workflowdispatch/`（payload 加 UploadID / FetchToken / FetchURL；workflow 名稱選擇）

- [ ] **Step 1: 寫 failing test**

```go
func TestRoutingDispatcher_UploadKind_UsesUploadGHAWorkflow(t *testing.T) {
	upDisp := &recDispatcher{}
	ghDisp := &recDispatcher{}
	rd := &RoutingDispatcher{GitHub: ghDisp, Upload: upDisp}
	err := rd.Dispatch(context.Background(), workflowdispatch.ClientPayload{
		SourceKind: "upload",
		UploadID:   "upl_x",
		FetchToken: "jwt-x",
	})
	if err != nil { t.Fatal(err) }
	if upDisp.calls != 1 || ghDisp.calls != 0 {
		t.Fatalf("expected upload dispatcher, got upload=%d github=%d", upDisp.calls, ghDisp.calls)
	}
}
```

- [ ] **Step 2: 跑 test，預期失敗**

Expected: FAIL

- [ ] **Step 3: 擴 `workflowdispatch.ClientPayload`**

加欄位（保 omitempty 不破既有 callers）：

```go
type ClientPayload struct {
	// existing ...
	SourceKind string `json:"source_kind,omitempty"` // "github" | "upload"
	UploadID   string `json:"upload_id,omitempty"`
	FetchToken string `json:"fetch_token,omitempty"`
	FetchURL   string `json:"fetch_url,omitempty"`
}
```

- [ ] **Step 4: 實作 `upload_dispatcher.go`**

```go
type UploadGHADispatcher struct {
	GH        *workflowdispatch.Client
	Workflow  string // "deploy-app-from-upload.yml"
}

func (u *UploadGHADispatcher) Dispatch(ctx context.Context, p workflowdispatch.ClientPayload) error {
	// override workflow name; payload 已含 FetchToken / FetchURL
	return u.GH.DispatchWorkflow(ctx, u.Workflow, p)
}
```

- [ ] **Step 5: 改 `RoutingDispatcher.Dispatch` 走 SourceKind 分派（不再讀 repo_url scheme）**

```go
func (r *RoutingDispatcher) Dispatch(ctx context.Context, p workflowdispatch.ClientPayload) error {
	switch p.SourceKind {
	case "upload":
		if r.Upload == nil { return errors.New("upload dispatcher not configured") }
		return r.Upload.Dispatch(ctx, p)
	case "github", "":
		// 既有 github / dev file:// 走原 dispatcher（dev mode 保留）
		return r.GitHub.Dispatch(ctx, p)
	default:
		return fmt.Errorf("unsupported source_kind: %s", p.SourceKind)
	}
}
```

並在 createapp.service Confirm path 傳入 `SourceKind` 與簽好的 `FetchToken` / `FetchURL`：

```go
token, _ := buildTokenSigner.Sign(ingestion.TokenClaims{
	TeamID: teamID, UploadID: src.Upload.UploadID, DeployRunID: runID,
})
payload.SourceKind = "upload"
payload.UploadID   = src.Upload.UploadID
payload.FetchToken = token
payload.FetchURL   = fmt.Sprintf("%s/v1/uploads/%s/archive", publicAPIURL, src.Upload.UploadID)
```

- [ ] **Step 6: 跑 test**

```bash
go test ./internal/server/services/createapp/ -v
go test ./internal/server/services/workflowdispatch/ -v
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/server/services/createapp/ internal/server/services/workflowdispatch/
git commit -m "feat(createapp): route upload source to GHA workflow variant"
```

---

### Task 15: `deploy-app-from-upload.yml` workflow

**Files:**
- Create: `deploy/workflows/deploy-app-from-upload.yml`
- Modify: `deploy/workflows/scripts/`（若 deploy-app.yml 有共用 script，沿用；否則內嵌 step）

- [ ] **Step 1: 讀既有 `deploy-app.yml` 的 build/push/callback structure**

```bash
cat deploy/workflows/deploy-app.yml
```

- [ ] **Step 2: 新增 workflow（鏡像 deploy-app.yml，差只在 source fetch step）**

```yaml
name: deploy-app-from-upload

on:
  workflow_dispatch:
    inputs:
      deploy_run_id: { required: true, type: string }
      app_slug:      { required: true, type: string }
      team_slug:     { required: true, type: string }
      upload_id:     { required: true, type: string }
      fetch_token:   { required: true, type: string }
      fetch_url:     { required: true, type: string }
      image_ref:     { required: true, type: string }
      ops_callback_url: { required: true, type: string }
      ops_token:        { required: true, type: string }

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    steps:
      - name: Fetch source archive from 0ops
        run: |
          mkdir -p ./src
          curl -fsSL --retry 3 \
               -H "Authorization: Bearer ${{ inputs.fetch_token }}" \
               -o /tmp/source.tar.zst \
               "${{ inputs.fetch_url }}"
          zstd -d /tmp/source.tar.zst -o /tmp/source.tar
          tar -xf /tmp/source.tar -C ./src
          # 確保 inspector 之 .git 存在；若 tar 缺則 init empty
          ( cd ./src && [ -d .git ] || git init -q )

      - name: Set up Buildpacks
        uses: buildpacks/github-actions/setup-pack@v5

      - name: Pack build
        working-directory: ./src
        run: |
          pack build "${{ inputs.image_ref }}" --builder paketobuildpacks/builder:base

      - name: Push to GHCR
        run: |
          echo "${{ secrets.GHCR_TOKEN }}" | docker login ghcr.io -u "${{ secrets.GHCR_USER }}" --password-stdin
          docker push "${{ inputs.image_ref }}"

      - name: Callback 0ops (live)
        if: success()
        run: |
          SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "${{ inputs.ops_token }}" -binary | xxd -p -c 256)
          curl -fsSL -X POST "${{ inputs.ops_callback_url }}" \
               -H "Content-Type: application/json" \
               -H "X-Ops-Signature: sha256=$SIG" \
               -d "$BODY"
        env:
          BODY: |
            {"deploy_run_id":"${{ inputs.deploy_run_id }}","state":"live","image_ref":"${{ inputs.image_ref }}"}

      - name: Callback 0ops (failed)
        if: failure()
        run: |
          # signed failed callback
          ...
```

callback signing & retry 沿用既有 `deploy-app.yml` 慣用做法（若該 file 有 reusable composite action，先抽出再共用）。

- [ ] **Step 3: 將 `deploy-app-from-upload.yml` 加入 server 端可分派 workflow 清單**

```bash
rg -n "deploy-app.yml" internal/ 2>/dev/null
```

更新對應允許清單 / 設定。

- [ ] **Step 4: 提交**

```bash
git add deploy/workflows/deploy-app-from-upload.yml internal/server/services/workflowdispatch/
git commit -m "feat(workflows): add deploy-app-from-upload.yml"
```

---

### Task 16: CLI tarball packer

**Files:**
- Create: `internal/cli/upload_pack.go`
- Create: `internal/cli/upload_pack_test.go`

- [ ] **Step 1: 寫 failing test**

```go
func TestPackDir_RespectsDockerIgnore(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "secret.env"), []byte("X=y"), 0644)
	os.WriteFile(filepath.Join(dir, ".dockerignore"), []byte("secret.env\n"), 0644)

	var buf bytes.Buffer
	got, err := PackDir(dir, &buf, PackOptions{MaxBytes: 10 * 1024 * 1024, MaxEntries: 100})
	if err != nil { t.Fatal(err) }

	files := listTarZst(t, &buf)
	if _, ok := files["secret.env"]; ok { t.Fatal("secret.env should be excluded") }
	if _, ok := files["main.go"]; !ok { t.Fatal("main.go missing") }
	if got.SHA256 == "" { t.Fatal("sha256 empty") }
}

func TestPackDir_RejectsOversize(t *testing.T) { /* MaxBytes=1KB；寫 10KB → err */ }
func TestPackDir_UsesGitLsFilesIfGitRepo(t *testing.T) { /* init git；ls-files only */ }
```

- [ ] **Step 2: 跑 test，預期失敗**

```bash
go test ./internal/cli/ -run TestPackDir -v
```

Expected: FAIL

- [ ] **Step 3: 實作 `PackDir`**

```go
type PackOptions struct {
	MaxBytes   int64
	MaxEntries int
}
type PackResult struct {
	SHA256   string
	Size     int64
	Entries  int
}

func PackDir(root string, out io.Writer, opt PackOptions) (PackResult, error) {
	// 1. 判斷 root 是否為 git repo（看 .git）
	// 2. 取 file list：git ls-files --recurse-submodules 或 walk + .dockerignore filter
	// 3. tar + zstd（io.MultiWriter to compute sha256 streaming）
	// 4. enforce size / entry caps
}
```

依賴 `github.com/klauspost/compress/zstd`（同 server）。

- [ ] **Step 4: 跑 test，預期通過**

```bash
go test ./internal/cli/ -run TestPackDir -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cli/upload_pack.go internal/cli/upload_pack_test.go
git commit -m "feat(cli): add local source tarball packer"
```

---

### Task 17: CLI multipart upload client

**Files:**
- Create: `internal/cli/uploads_client.go`
- Create: `internal/cli/uploads_client_test.go`

- [ ] **Step 1: 寫 failing test（用 httptest.Server 模擬 server）**

```go
func TestUploadsClient_UploadHappy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert multipart, return 201 + upload_id JSON
	}))
	defer ts.Close()

	c := &UploadsClient{BaseURL: ts.URL, Token: "tkn"}
	res, err := c.Upload(context.Background(), UploadArgs{Reader: bytes.NewReader([]byte("...")), Format: "tar.zst", SHA256: "deadbeef"})
	if err != nil { t.Fatal(err) }
	if res.UploadID == "" { t.Fatal("upload_id empty") }
}
```

- [ ] **Step 2: 跑 test，預期失敗**

Expected: FAIL

- [ ] **Step 3: 實作 client**

`mime/multipart` 編 form；`Authorization: Bearer <token>`；streaming body（不 buffer 整檔）。

- [ ] **Step 4: 跑 test**

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cli/uploads_client.go internal/cli/uploads_client_test.go
git commit -m "feat(cli): add uploads client"
```

---

### Task 18: CLI `0ops apps create --source`

**Files:**
- Modify: `internal/cli/apps_create.go`（既有 `--repo-url`；加 `--source`）
- Modify: `internal/cli/apps_create_test.go`

- [ ] **Step 1: 讀現況**

```bash
rg -n "func.*AppsCreate|--repo-url|repo-url" internal/cli/ | head
```

- [ ] **Step 2: 寫 failing test**

```go
func TestAppsCreate_SourceLocalPath_UploadsAndPosts(t *testing.T) {
	// 用 httptest.Server 接 /v1/uploads 與 /v1/apps/preview / confirm
	// CLI 跑 0ops apps create --source <tmp dir>
	// 驗：先 POST /v1/uploads，後 POST /v1/apps/preview 帶 source.type=upload
}

func TestAppsCreate_SourceGitURL_DispatchedToGitHub(t *testing.T) { /* 不打 uploads */ }
func TestAppsCreate_RepoURLDeprecationWarning(t *testing.T) { /* stderr 含 deprecated */ }
```

- [ ] **Step 3: 跑 test，預期失敗**

Expected: FAIL

- [ ] **Step 4: 實作 `--source` 分派**

```go
type sourceClass int
const (
	sourceLocalPath sourceClass = iota
	sourceUploadID
	sourceGitHubURL
	sourceFileURL // dev-only
)

func classifySource(s string) sourceClass {
	switch {
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "./"), strings.HasPrefix(s, "../"):
		return sourceLocalPath
	case strings.HasPrefix(s, "upload://"):
		return sourceUploadID
	case strings.HasPrefix(s, "https://github.com/"), strings.HasPrefix(s, "git@github.com:"):
		return sourceGitHubURL
	case strings.HasPrefix(s, "file://"):
		return sourceFileURL
	}
	return -1
}

// 主流程：
//   class == sourceLocalPath -> pack + upload -> source.type = upload
//   class == sourceUploadID -> source.type = upload, upload_id = strings.TrimPrefix(s, "upload://")
//   class == sourceGitHubURL -> source.type = github
//   class == sourceFileURL -> 維持既有 repo_url 路徑（dev only）
```

`--repo-url` 仍接受但印 `deprecated` warning 至 stderr。

- [ ] **Step 5: 跑 test，預期通過**

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/cli/apps_create.go internal/cli/apps_create_test.go
git commit -m "feat(cli): wire --source flag with bare-path dispatch"
```

---

### Task 19: Upload GC reconciler

**Files:**
- Create: `internal/server/services/reconciler/upload_gc.go`
- Create: `internal/server/services/reconciler/upload_gc_test.go`
- Modify: `cmd/server/main.go`（mount loop）

- [ ] **Step 1: 寫 failing test**

```go
func TestUploadGC_DeletesExpiredAndMarksRow(t *testing.T) {
	repo := newFakeUploadRepo()
	store := &ingestion.Store{Root: t.TempDir()}
	repo.InsertUpload(ctx, db.Upload{ID: "upl_x", TeamID: "team_a", Status: "received", ExpiresAt: time.Now().Add(-time.Hour)})
	_ = createIngestTreeUnder(store.Root, "team_a/upl_x")

	gc := &UploadGC{Repo: repo, Store: store}
	if err := gc.RunOnce(ctx); err != nil { t.Fatal(err) }

	got, _ := repo.GetUpload(ctx, "team_a", "upl_x")
	if got.Status != "gc'd" { t.Fatalf("status: %s", got.Status) }
	if _, err := os.Stat(filepath.Join(store.Root, "team_a", "upl_x")); !os.IsNotExist(err) {
		t.Fatalf("tree should be removed; err=%v", err)
	}
}
```

- [ ] **Step 2: 跑 test，預期失敗**

Expected: FAIL

- [ ] **Step 3: 實作 `UploadGC.RunOnce`**

- `ListExpiredUploads(ctx, limit=100)`
- 每筆：先 `store.Delete(ctx, team, id)` → 再 `repo.MarkUploadGCd(ctx, id)`
- 失敗：log + continue（不阻斷整輪）

`Run(ctx)`：每小時跑一次 ticker。

- [ ] **Step 4: main.go 啟動 loop**

```go
go (&reconciler.UploadGC{Repo: repo, Store: ingestionStore}).Run(ctx)
```

- [ ] **Step 5: 跑 test，預期通過**

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/server/services/reconciler/upload_gc.go internal/server/services/reconciler/upload_gc_test.go cmd/server/main.go
git commit -m "feat(reconciler): add upload GC loop"
```

---

### Task 20: 配額與 rate-limit 整合

**Files:**
- Modify: `internal/server/middleware/ratelimit/` 或既有 quota 模組（先 grep）
- Modify: `internal/server/uploads.go`（在 handler 內檢查配額）
- Modify: `internal/server/uploads_test.go`

- [ ] **Step 1: grep 既有 quota 抽象**

```bash
rg -n "PlanQuotas|DefaultPlanQuotas|EnforceQuota" internal/ | head
```

- [ ] **Step 2: 寫 failing test**

```go
func TestPostUploads_413WhenTeamInertBytesExceeded(t *testing.T) { /* set per-team cap=1KB；first upload 800B OK；second upload 800B → 507 */ }
func TestPostUploads_429WhenRateLimitHit(t *testing.T) { /* same team 201/min ceiling */ }
```

- [ ] **Step 3: 實作**

- 在 handler 進入前查 `repo.SumInertBytes(teamID)`；對比 `plan.MaxInertBytes`
- 接 ratelimit middleware 加 endpoint group `uploads`

- [ ] **Step 4: 跑 test**

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/server/uploads.go internal/server/uploads_test.go internal/server/middleware/ratelimit/
git commit -m "feat(quota): enforce upload size, count and team inert quotas"
```

---

### Task 21: Metrics + audit

**Files:**
- Modify: `internal/server/observability/metrics.go`
- Modify: `internal/server/services/audit/`（事件常量）
- Modify: `internal/server/uploads.go`、`createapp/service.go` 對應點埋點

- [ ] **Step 1: 寫 failing test**

```go
func TestMetrics_UploadCounter(t *testing.T) {
	m := NewMetrics()
	m.ObserveUpload("success", "", 1024, 200*time.Millisecond)
	// fetch /metrics handler output; check counter / histogram present
}
```

- [ ] **Step 2: 跑 test，預期失敗**

Expected: FAIL

- [ ] **Step 3: 加 metrics**

`app_source_upload_total{result,reject_reason}`、`app_source_upload_size_bytes`、`app_source_upload_duration_seconds`、`app_source_inert_bytes{team_id}`、`app_source_pinned_bytes{team_id}`、`app_source_gc_deleted_total`。

deploy_run counter 加 `source_kind` label。

- [ ] **Step 4: audit events**

新增常量：
- `app_source.upload.created`
- `app_source.upload.pinned`
- `app_source.upload.cross_team_access_denied`
- `app_source.upload.malicious`
- `app_source.upload.expired`
- `app_source.upload.gc_d`

- [ ] **Step 5: 埋點**

uploads handler、createapp.Confirm、reconciler.UploadGC、ingestion.Store reject path。

- [ ] **Step 6: 跑 test**

```bash
go test ./internal/server/... -run "Metrics|Audit" -v
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/server/observability/ internal/server/services/audit/ internal/server/uploads.go internal/server/services/createapp/ internal/server/services/reconciler/
git commit -m "feat(obs): add upload metrics and audit events"
```

---

### Task 22: e2e 透過 compose + Makefile

**Files:**
- Create: `tasks/app-source-ingestion-e2e.sh`
- Modify: `Makefile.tasks`（或 root Makefile）
- Modify: `compose.yaml`（若 server pod 需要新 ENV: `APP_SOURCE_INGEST_ROOT` / `OPS_BUILD_TOKEN_SECRET`，加入 service env 區）
- Modify: `.env.example`（加新 ENV 註解）

- [ ] **Step 1: 加 compose env**

`compose.yaml` 之 server service：

```yaml
environment:
  - APP_SOURCE_INGEST_ROOT=/var/lib/0ops/uploads
  - OPS_BUILD_TOKEN_SECRET=${OPS_BUILD_TOKEN_SECRET}
volumes:
  - app-source-uploads:/var/lib/0ops/uploads
```

加 named volume `app-source-uploads`。

- [ ] **Step 2: 寫 e2e script `tasks/app-source-ingestion-e2e.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail
# Exercises: seed-cli-token → 0ops apps create --source ./examples/node-demo → wait live
TOKEN=$(./bin/seed-cli-token --team-slug demo --user-id u_1)
./bin/0ops auth login --token "$TOKEN"
./bin/0ops apps create --source ./examples/node-demo --slug e2e-upload --yes
# Poll deploy_run state via /v1/teams/demo/apps/e2e-upload/deploys/latest 直至 live or timeout
```

- [ ] **Step 3: 加 Makefile target**

```makefile
m6-app-source-e2e: dev-up dev-seed-cli-token  ## 跑一次 upload-based create_app at live
	bash tasks/app-source-ingestion-e2e.sh
```

- [ ] **Step 4: 跑**

```bash
make m6-app-source-e2e
```

Expected: deploy_run terminal = `live`；script exit 0。

如失敗：依 lessons L004 規則 debug（必走 compose + Makefile，不在 host 直跑 binary）。

- [ ] **Step 5: 提交**

```bash
git add tasks/app-source-ingestion-e2e.sh Makefile.tasks compose.yaml .env.example
git commit -m "test(app-source-ingestion): add upload-based e2e via compose"
```

---

### Task 23: docs sync + CLI deprecation warning

**Files:**
- Modify: `docs/features/app-source-ingestion/spec.md`（將 Open Questions 中已釘定者收斂）
- Modify: `docs/0ops-plan.md`（若有「app source」字眼之段落）
- Modify: `internal/cli/apps_create.go`（deprecation warning 文案）
- Create: `docs/features/app-source-ingestion/release/2026-05-20-cli-source-flag-migration.md`

- [ ] **Step 1: 寫 release sub-spec**

`docs/features/app-source-ingestion/release/2026-05-20-cli-source-flag-migration.md`：

```markdown
# CLI `--source` flag migration

## 對外變更
- 新增：`0ops apps create --source <path|url|upload://...>`
- Deprecated：`--repo-url`（保留至 M8；CLI 印 deprecation warning）
- API：`AppCreateRequest.source` 為新欄位；`repo_url` + `ref` 自動 normalize 為 `source.type=github`

## migration cheat-sheet
| 舊 | 新 |
|---|---|
| `--repo-url https://github.com/foo/bar --ref main` | `--source https://github.com/foo/bar --ref main` |
| 無對應 | `--source ./my-app`（本地路徑自動 upload） |
| 無對應 | `--source upload://upl_xxx`（複用既有 upload） |

## 對外 SLO
- upload p95 < 30 秒（100MB cap，10MB/s 下載基準）
- preview→live 不受新 path 影響（仍 < 10 分鐘 p50）
```

- [ ] **Step 2: 更新 spec.md Open Questions**

把 spec § 16 中已釘定者標 `[Resolved 2026-05-20]`。

- [ ] **Step 3: CLI deprecation warning**

```go
if flagRepoURL != "" {
	fmt.Fprintln(os.Stderr, "warning: --repo-url is deprecated; use --source. See docs/features/app-source-ingestion/release/2026-05-20-cli-source-flag-migration.md")
}
```

- [ ] **Step 4: 提交**

```bash
git add docs/features/app-source-ingestion/ internal/cli/apps_create.go docs/0ops-plan.md
git commit -m "docs(app-source-ingestion): finalize spec + CLI deprecation warning"
```

---

## Self-Review 檢核（plan 寫完後本人跑）

### Spec coverage 對照

| Spec section | Task |
|---|---|
| §1 結論 — sum type、ingest tree、build path、ADR-0012 dev path 保留 | T2/T6/T15/T11 |
| §2.1 包含項目逐條 | T1-T22 cover 全部 |
| §3 檔案結構 | T2/T5/T6/T7/T8/T9/T10/T15/T16/T17/T18/T19 |
| §4 Source DTO 與驗證規則 | T2/T11 |
| §5 Upload API | T8/T20 |
| §6 CLI 行為與 tarball 規則 | T16/T17/T18 |
| §7 ingest tree 佈局與 preview/confirm 串接 | T6/T12/T13 |
| §8 GHA workflow 變體 + JWT | T7/T9/T14/T15 |
| §9 路徑安全與租戶隔離 | T6 |
| §10 生命週期與 GC | T5/T19 |
| §11 配額與限制 | T20 |
| §12 runtime.AssertProductionSafe | T4 |
| §13 與既有 ADR 對齊 | T1/T4/T11/T14 |
| §14 失敗矩陣 | T8/T9/T11/T13/T15 |
| §15 觀測 | T21 |
| §16 Open Questions | T23 |

無缺漏。

### Placeholder 掃描

- 未出現 "TBD" / "TODO" / "implement later"
- T6 Step 3 有 "// detect framework based on files in tree" → 已指明用既有 detector，引用 RepoMetadata；可在 T10 Step 3 補完 detector 共用。
- T8 Step 3 "io.Copy" / sha256 streaming — 細節留給實作者，但介面與資料路徑已固化。
- T15 callback signing `...` 是 reusable composite action sharing 之 marker，工作量明確；不算 placeholder。

### 型別／介面一致性

- `Source` / `SourceKind` 在 T2 定義；T10-T18 一致使用。
- `Inspector` interface 在 T10 定義 (`Inspect(ctx, src Source, teamID string)`)；T12 service 使用一致。
- `Dispatcher` 既有介面 T14 擴 payload 而非改介面；既有 callers 不破。
- `Upload` struct 在 T5 定義；T6/T8/T9/T10/T13/T19 一致使用。
- `TokenClaims` 在 T7 定義；T9/T14 一致使用。

無不一致。

---

## 結束

Plan 完成。下一步走 `superpowers:subagent-driven-development`：fresh subagent 跑 T1 → 評審 → merge → T2 → ...
