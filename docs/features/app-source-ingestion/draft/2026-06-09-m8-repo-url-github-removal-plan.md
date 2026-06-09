# M8 — Remove deprecated `repo_url` github alias (keep dev `file://`)

> 狀態：Done（2026-06-09 落地；權威狀態見 spec § 17 + release migration doc）
> 來源：`spec.md` § 16 Q6 + § 17（Target M8）；release migration doc
> 範圍決策（2026-06-09）：窄刪 github alias + MCP 補 source；保留 ADR-0012 dev `file://`

## 1. 結論

`repo_url` 承載兩種語意，共用 `AppCreateRequest.RepoURL`/`--repo-url`：

- **github URL alias**（deprecated，ADR-0013 §4.2）→ **本次移除**，github 只走 `source` sum type。
- **`file://` dev local repo**（ADR-0012，spec § 2.2「不改動」）→ **保留**，仍走 `repo_url`。

移除 server 端 `repo_url(github) → Source{github}` normalize shim。因 MCP `create_app_preview`
目前**只收 repo_url、無 source 欄位**，移除 shim 前必須先給 MCP 補 `source`，否則 MCP github
建立無替代路徑而回歸。

移除後行為：

| 輸入 | 結果 |
|---|---|
| `source = github\|upload`（API/CLI/MCP） | OK |
| `repo_url = https://github.com/...` 或 `git@github.com:...` | **ERROR：use source** |
| `repo_url = file://...`（dev, `LOCAL_FILE_REPO_ENABLED=true`） | OK（保留）|
| `repo_url = file://...`（production） | reject（不變）|

## 2. 變更清單

### 程式碼

1. **`internal/server/apps.go` `validateAppCreateRequest`**
   - 移除 legacy github 分支（normalize 成 `Source{github}`）。
   - github repo_url → 寫 `CodeUnsupportedSource`「repo_url github source removed; use source」。
   - 保留 file:// 分支不動；更新函式 doc comment。

2. **`internal/server/services/createapp/service.go` `validateSource`**
   - 移除 legacy `repo_url` 之 github case；只留 file:// dev。更新 comment。

3. **`internal/cli/root.go`**
   - `sourceKindUnset`（只給 `--repo-url`）分支內依 `classifySource(createRepoURL)` 二分：
     github → 硬錯導向 `--source`；file:// → 保留設 `RepoURL`；其他 → 錯。
   - 移除「will be removed in M8」warning（github 改硬錯）。
   - `--repo-url` flag help 改為「dev file:// only」。

4. **`internal/mcp/server/server.go`**
   - `createAppPreviewInput` 加 `Source *dto.Source`；`repo_url`/`ref` 改為 dev file:// only。
   - 驗證改為 require team_slug + slug + (source 或 repo_url)；透傳 `Source` 進 `AppCreateRequest`。
   - server 端 `validateAppCreateRequest` 仍為唯一權威 gate。

5. **`internal/shared/dto/apps.go`**
   - 更新 `RepoURL`/`Ref` doc comment：dev file:// only（ADR-0012）；github via `Source`。欄位保留。

### 測試（TDD）

- server `apps_test.go`：repo_url=github → 4xx（原本 normalize+成功）；file:// dev 與 source 仍綠。
- CLI `apps_test.go` / `apps_create_source_test.go`：`--repo-url <github>` 硬錯；`--repo-url file://` 與 `--source` 不變。
- MCP `server_test.go`：`create_app_preview` 帶 `source.github`/`source.upload` 透傳；schema lint 契約綠。
- service `service_test.go`：移除 github repo_url legacy 期望。

### 文件

- release migration doc：github-via-repo_url 標 **Removed（M8）**。
- `spec.md` § 16 Q6 + § 17：狀態翻 Done。
- `tasks/todo.md`：Q6 / M8 收尾。

## 3. 不在範圍

- ADR-0012 dev file:// 機制、LocalBuildDispatcher：不動。
- 新增 source kind（gitlab/s3/oci）：不做。
- `AppRef.RepoURL`（stored app 列表 DTO，github+file:// 皆合法）：不動。
- `routing_dispatcher.go`（stored app 內部分派）：不動。
