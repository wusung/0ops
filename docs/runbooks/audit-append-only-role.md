# Runbook：audit_log append-only role provisioning

> 來源：`docs/features/audit-export-and-integrity/spec.md` § 5 / ADR-0015 § 3.1。
> 對象：production / staging 部署操作者。dev 由 compose 的 `provision-app-role`
> 服務自動處理，本 runbook 不適用 dev。

## 背景

migration `00014_audit_append_only_roles.sql` 建立三個 **NOLOGIN** 權限角色並設
grant/revoke，於 Postgres 權限層強制 append-only（hard rules #1/#2）：

| 角色 | audit_log 權限 | 用途 |
|---|---|---|
| `"0ops_app"` | `SELECT, INSERT`（**無 `UPDATE`/`DELETE`**）；`audit_chain_head` 可 `UPDATE` | runtime 連線 envelope |
| `"0ops_migrate"` | DDL 全權 | goose migration 身分 |
| `"0ops_archive"` | `SELECT, INSERT, DELETE` + partition `DROP` | rollover / archive job |

migration 不寫入任何密碼：角色為純權限角色，由本 runbook 在每個環境掛上登入身分。

## 連線切換（與 migration 上線同批）

> **關鍵**：runtime 連線必須切到 `"0ops_app"`，否則對既有 owner 連線撤權無效
> （owner 仍可改/刪）。migration 上線與連線切換要同批，spec § 5.1。

1. 套用 migration 至目標 DB：

   ```sh
   goose -dir src/migrations postgres "$DATABASE_URL" up
   ```

2. 建立 runtime 登入身分（兩種等效做法，擇一）：

   - **(a) 直接給 `"0ops_app"` 登入能力**（最少角色）：

     ```sql
     ALTER ROLE "0ops_app" WITH LOGIN PASSWORD :'pw';
     ```

   - **(b) 另建登入帳號並 inherit envelope**（憑證輪替較易）：

     ```sql
     CREATE ROLE app_runtime LOGIN PASSWORD :'pw' IN ROLE "0ops_app";
     -- app_runtime 本身不持任何直接權限，效權 = "0ops_app" envelope
     ```

   兩者皆 **不可為 superuser**（superuser 繞過所有權限檢查，append-only 失效）。

3. 將 runtime 連線字串設為該登入身分：

   ```sh
   # server 讀 APP_DATABASE_URL 優先於 DATABASE_URL（internal/server/db.ConfigFromEnv）
   APP_DATABASE_URL=postgres://0ops_app:<pw>@<host>:5432/<db>?sslmode=require
   ```

   `DATABASE_URL` 保留給 migrate / ops 工具（superuser 或 `"0ops_migrate"`）。

4. archive / rollover job 以 `"0ops_archive"` 登入身分執行（同 (b) 模式），不得
   以 runtime 連線跑。

## 驗證

```sql
-- 以 runtime 身分連線後，下列必須被拒：
UPDATE audit_log SET action = 'x';   -- ERROR: permission denied
DELETE FROM audit_log;               -- ERROR: permission denied
-- 下列必須允許：
INSERT INTO audit_log (...);         -- OK（寫入路徑）
UPDATE audit_chain_head SET ...;     -- OK（tip 維護）
```

整合測試 `TestAuditLogAppendOnlyRoleDeniesMutation`
（`src/internal/server/db/audit_append_only_test.go`）以同一權限模型自動驗證。

## 殘留風險

持 `"0ops_migrate"` / `"0ops_archive"` / superuser 憑證者仍可改/刪 +
重算整鏈；該等憑證屬信任核心，不在 append-only 防護對象內（spec § 9、ADR-0015 §
6.3）。竄改仍可由 `0ops audit verify` 之 hash chain 重算偵測。
