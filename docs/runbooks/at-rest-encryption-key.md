# Runbook：Secret at-rest 加密金鑰管理 / 輪替

> 來源：`docs/features/security-hardening/spec.md` § 9（解 threat-model SE2）。
> 範圍：K8s native `Secret` 在 K3s datastore 的 **at-rest 加密層金鑰**。
> 不含：secret **內容** 的 rotation（A–D 類，屬 `secrets-management` § 5）；兩者正交。

## 1. 結論（先讀）

- application 端 PAT / device token 以 `argon2id` 雜湊存 DB（不可逆，非加密；已具備）。
- 共享 / 外部 secret 以 K8s native `Secret`（base64，**非加密**）儲存，落於 K3s
  datastore（kine on Postgres，ADR-0004）。base64 **不是** 加密。
- at-rest 保護取決於 datastore 層：對 `secrets` 資源啟用 K8s
  `EncryptionConfiguration`，與 / 或 datastore（Postgres）卷加密。
- 本 runbook 文件化該層的金鑰所在與 90 天輪替程序。

## 2. 採用方案（部署時擇定並回填）

| 方案 | 說明 | 金鑰所在 |
|---|---|---|
| A. EncryptionConfiguration | 對 `secrets` 資源加密寫入 kine（aescbc/secretbox provider） | 控制平面主機 `encryption-config.yaml`，由 ops KMS / 受控檔案注入 |
| B. datastore 卷加密 | Postgres 資料卷整卷加密（LUKS / 雲端卷加密） | 卷加密金鑰於雲端 KMS / 主機 TPM |

二擇一或並用；**實際採用方案須在此節回填**，模板見
`deploy/security/encryption-config.example.yaml`。

## 3. 金鑰所在硬性規則（spec § 13 #9）

- 加密金鑰**不得**與被加密資料同一信任域。
- 金鑰**不得**入 git、**不得**入 K8s `Secret` 自身。
- 存於 cluster 外：ops 管理之 KMS，或受控檔案 + 嚴格 OS 權限（control-plane 限定，`0600`）。

## 4. 輪替程序（方案 A，週期 90 天）

K8s `EncryptionConfiguration` 多 key 輪替（新 key 置首、舊 key 保留供解密）：

1. **入帳（start）**：寫 audit `secret_rotate_start`，subject＝`system:at-rest-key`
   （復用 `secrets-management` § 9.1 既有 action）。
2. 產新 32-byte key：`head -c 32 /dev/urandom | base64`，由 ops KMS 注入。
3. 將新 key **置於 `keys` 列首**（舊 key 保留在後），重啟 kube-apiserver 套用。
   此時新寫入用新 key，舊資料仍可由舊 key 解密。
4. 重寫全 secret 觸發以新 key 重新加密：
   `kubectl get secrets -A -o yaml | kubectl replace -f -`
5. 確認全 secret 已以新 key 重寫後，從 `keys` 移除舊 key，再次重啟 apiserver。
6. **入帳（finalize）**：寫 audit `secret_rotate_finalize`，subject＝`system:at-rest-key`。

> 輪替不影響 application 端 token TTL（屬 `auth-and-rbac` / security-hardening § 7）。

## 5. 驗證

| 驗證項 | 方式 | 通過條件 |
|---|---|---|
| at-rest 金鑰文件化 | 查本 runbook § 2 | 已回填實採方案與金鑰所在 |
| 加密金鑰輪替入帳 | 跑一次輪替 | audit 含 `secret_rotate_start` + `secret_rotate_finalize`（subject＝`system:at-rest-key`） |

## 6. 與其他文件接合

- secret 內容 rotation（A–D 類）：`docs/features/secrets-management/spec.md` § 5
- audit action `secret_rotate_*`：`secrets-management` § 9.1
- 模板 manifest：`deploy/security/encryption-config.example.yaml`
- datastore 拓樸：`docs/features/postgres-ha-and-dr/spec.md`（最終卷加密決策依此）
