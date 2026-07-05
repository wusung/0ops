---
cadence: weekly
source: docs/adrs/0002-idempotency-and-compensation.md
slug: preview-confirm-idempotency
---

## 中文

# 為什麼 preview→confirm 冪等性寫在 backend，而不是 UI 慣例

**限制**：0ops 的寫入路徑同時面對三類不可信來源——CLI（`--yes` 腳本化重試）、MCP/LLM（claude code、codex、copilot 對 tool description 遵守度不一，可能跳過 preview、盲目重試、複用舊 `preview_id`）、以及 GitHub Actions 回調（重送、丟失）。這些寫入的副作用跨四個系統：GitHub Actions build、GHCR image push、Cloudflare DNS/tunnel、gitops repo commit。其中 image push 與 tunnel binding 不可逆。ADR-0002 把問題釘死：如果冪等性只是「客戶端該先呼叫 preview」的約定，任何一個不守約的 LLM 都能讓同一個 app 被建立兩次、image 被 push 兩次。約定不是防線，結構才是。

**選項**：（A1）`preview_id` 兼作 idempotency key，client 拿到 preview 即取得「行動授權」與「重試令牌」；（A2）獨立的 `Idempotency-Key` header，走 Stripe/RFC 9110 慣例；（A3）對 args 做 request hash；（A4）不設冪等，每次重試都重做副作用。重試行為上：（B1）`consumed_at != null` 直接回放 `last_result`；（B2）回 409 要 client 重新 preview；（B3）重做。

**取捨**：ADR-0002 選 **A1 + B1**，理由是 LLM 客戶端只需理解一個概念——「拿到 preview_id 即可安全重試」。這不是 UI 慣例，而是 backend 與 sqlc codegen 層的結構性強制。`preview(team_id, idempotency_key)` 是 DB 唯一索引；confirm 在 transaction 內 `SELECT ... FOR UPDATE` 鎖 preview row，並在同一 tx 重檢先決條件。程式碼把三種非法重試一次擋掉：`internal/server/services/createapp/service.go:178` 這一行 `preview.TeamID != teamID || preview.ActorUserID != actorUserID` 直接把跨 team 或 actor 不符的 preview 當成 `ErrPreviewNotFound`（接續 ADR-0001 的 enumeration 防範，對外表現為 404，不洩漏存在性）；緊接著 consumed 的 preview 回放 `last_result`、過期的回 `ErrPreviewExpired`。放棄的是 A2 的擴展彈性與 B4 的 args 比對精細度——v1 不值得那個複雜度。得到的是：副作用恰好執行一次，LLM 盲目重試也安全。這就是 agent-native safe writes 的護城河：不是叫 agent 小心，而是讓它無法犯錯。

## English

# Why preview→confirm idempotency lives in the backend, not in UI convention

**Constraint**: 0ops write paths take orders from three untrusted callers at once—the CLI (scripted `--yes` retries), MCP/LLM clients (claude code, codex, copilot honor tool descriptions unevenly; an LLM may skip preview, retry blindly, or reuse a stale `preview_id`), and GitHub Actions callbacks (resends, drops). Each write fans out side effects across four systems: GHA build, GHCR image push, Cloudflare DNS/tunnel, and a gitops commit. Image push and tunnel binding are irreversible. ADR-0002 nails the problem down: if idempotency is merely a "the client should call preview first" convention, any one misbehaving LLM can create the same app twice and push the same image twice. A convention is not a defense; structure is.

**Options**: (A1) `preview_id` doubles as the idempotency key, so the client gets both authorization and a retry token from one object; (A2) a separate `Idempotency-Key` header, Stripe/RFC 9110 style; (A3) a request hash over args; (A4) no idempotency at all, redoing side effects on every retry. On retry behavior: (B1) replay `last_result` when `consumed_at != null`; (B2) return 409 and force a fresh preview; (B3) re-execute every time.

**Trade-off**: ADR-0002 picks **A1 + B1**, because an LLM client only has to grasp one concept—"holding a preview_id means you can retry safely." That guarantee is not a UI convention; it is structurally enforced at the backend and sqlc codegen layer. `preview(team_id, idempotency_key)` is a DB unique index; confirm locks the preview row with `SELECT ... FOR UPDATE` inside a transaction and re-checks preconditions in that same tx. The code rejects three illegal retries in one place: `internal/server/services/createapp/service.go:178`—the guard `preview.TeamID != teamID || preview.ActorUserID != actorUserID` collapses any cross-team or wrong-actor preview into `ErrPreviewNotFound` (continuing ADR-0001's enumeration defense, surfaced as a 404 that leaks no existence); the consumed branch right after replays `last_result`, and the expired branch returns `ErrPreviewExpired`. What we give up is A2's extensibility and B4's fine-grained args comparison—v1 doesn't earn that complexity. What we get: side effects run exactly once, and even a blindly retrying LLM stays safe. That is the moat for agent-native safe writes: we don't ask the agent to be careful; we make it structurally unable to get it wrong.
