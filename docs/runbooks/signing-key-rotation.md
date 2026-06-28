# Runbook：簽章金鑰輪替（callback / webhook / cosign 退路）

> 來源：`docs/features/supply-chain-security/spec.md` § 8（接 AU4 / AU5）；ADR-0017。
> 範圍：HMAC 簽章金鑰（callback 主簽章、emergency fallback、GitHub webhook secret）
> 與 cosign key-based 退路私鑰的**輪替流程**。
> 不含：簽章 / 驗章邏輯本身（已 ship，不在此改）；secret at-rest 加密本體
> （屬 `secrets-management`，見 `at-rest-encryption-key.md`）。

## 1. 結論（先讀）

- 所有簽章金鑰輪替走「**新舊雙窗並驗**」：任一時點拒收的合法請求數必為 **0**
  （spec § 11 驗證項 / hard rule #9）。
- 雙窗時長以「在途憑證最大存活時間」為下界。`ops_token` TTL ≤ 20 min，故雙窗
  取 **30 min**，涵蓋所有在途請求。
- 每次輪替的 start / finalize 各寫一筆 `audit_log`（`secret_rotate_start` /
  `secret_rotate_finalize`），失敗則 `outcome=failure` 並中止，不可半途移除舊 key。
- 週期 **90 天**，四把 key 對齊同一節奏。

## 2. 金鑰清單

| Secret | 角色 | 既有狀態 | 雙窗 |
|---|---|---|---|
| `OPS_TOKEN_SIGNING_SECRET` | callback 主簽章 key | 已 ship、90 天輪替 | 30 min |
| `OPS_CALLBACK_SECRET` | emergency fallback | 90 天輪替（v2 移除後退役） | 30 min |
| GitHub webhook secret | 防假 push 偽造（AU4） | HMAC 驗章已 ship、輪替流程本 runbook 補 | 30 min |
| cosign key-based 退路私鑰 | 離線 / 自架 Sigstore 未就緒時的簽章退路 | 本 spec 引入；keyless 為主時無此 key | n/a（非請求驗證，見 § 6）|

> keyless（GitHub OIDC + Fulcio + Rekor）為簽章主路徑，**無長期私鑰**，故無輪替
> 負擔。只有啟用 key-based 退路時才有 § 6 的私鑰生命週期。

## 3. 通用雙窗程序（HMAC 類：callback / fallback / webhook）

後端在輪替期支援「新舊雙 secret 並驗」：收到請求時，先以新 key 驗，失敗再以舊 key
驗，任一通過即接受。

1. **產新 key**：`openssl rand -hex 32`。
2. **start**：把新 key 加為 K8s `Secret` 的第二 version（舊 key 保留）；後端進入
   雙驗模式。寫 `audit_log`（`action=secret_rotate_start`、`source=system`、
   `outcome=success`，args 記 key 名與 version，**不記 key 值**）。
3. **等雙窗 30 min**：涵蓋所有在途 `ops_token`（TTL ≤ 20 min）/ 在途 callback。
4. **切換簽發端**：把簽發 / 發送端（GHA workflow secret、webhook 設定）改用新 key。
5. **finalize**：確認新 key 流量正常後移除舊 key version；後端回到單驗。寫
   `audit_log`（`action=secret_rotate_finalize`）。
6. **驗證**：§ 5 檢查表。

### 3.1 GitHub webhook secret 特例

GitHub repo webhook 不支援多 secret 並存，故「雙窗」落在**後端**：

1. 後端先進入「新舊雙 secret 並驗」模式（部署新 key 為第二 version，30 min）。
2. 在 GitHub repo webhook 設定端把 secret 換成新值（單點切換）。
3. 30 min 後後端移除舊 secret，回到單驗。

切換瞬間 GitHub 已用新 key 簽、後端雙驗仍接受 → 拒收合法請求數 = 0。

## 4. callback 主簽章（`OPS_TOKEN_SIGNING_SECRET`）細節

- 簽發端：backend 簽 `ops_token`；GHA workflow 以該 token 對 callback payload 做
  HMAC（`X-0ops-Signature: sha256=...`）。
- 在途憑證：已簽發但未用完的 `ops_token`（TTL ≤ 20 min）。雙窗 30 min 必須 ≥ TTL。
- 程序同 § 3；§ 4 步驟 4「切換簽發端」即 backend 改用新 key 簽發後續 `ops_token`。

## 5. 輪替後驗證檢查表

- [ ] 雙窗期間 callback / webhook 端到端各打一筆合法請求 → HTTP 2xx（拒收數 0）。
- [ ] `audit_log` 有對應 `secret_rotate_start` 與 `secret_rotate_finalize` 兩筆。
- [ ] 移除舊 key 後，以舊 key 簽的請求被拒（401，證明舊 key 已失效）。
- [ ] 以新 key 簽的請求通過。
- [ ] 無 `callback rejected: invalid signature` 之合法請求誤殺（查 server log）。

## 6. cosign key-based 退路私鑰

僅在啟用 key-based 退路（自架 Sigstore / 離線場景）時適用；keyless 為主時跳過本節。

1. `cosign generate-key-pair`（或 KMS-backed）產新 key pair。
2. 新私鑰存單一 K8s `Secret`（不入 git，§ `at-rest-encryption-key.md` 規則）。
3. **重簽 / 共存**：對仍在用的 image digest 以新 key 重新 `cosign sign`；admission
   policy 的 `authorities` 暫時同時信任新舊公鑰（雙窗），確認新簽章覆蓋後移除舊公鑰。
4. 寫 `audit_log`（`secret_rotate_start` / `secret_rotate_finalize`）。
5. 私鑰外洩時為**緊急輪替**：立即重簽 + 撤舊公鑰，不等 90 天週期。

> key-based 私鑰外洩屬高風險事件；緊急輪替後須複審近期所有以舊 key 簽的 image。
