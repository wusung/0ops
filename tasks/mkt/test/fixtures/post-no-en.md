---
cadence: weekly
source: docs/adrs/0002-idempotency-and-compensation.md
slug: preview-confirm-idempotency
---

## 中文

# 為什麼寫入操作要先 preview 再 confirm

**限制**：agent 的寫入不可逆，決策錨定 ADR-0002。

**選項**：UI 約定冪等、backend 冪等鍵、或不做冪等。

**取捨**：選 backend 冪等鍵，放棄 UI 端便利。
