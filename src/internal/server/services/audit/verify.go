package audit

import (
	"bytes"
	"time"
)

// Verify (audit-export-and-integrity spec § 7 / ADR-0015 § 3.4) recomputes a
// per-(team, month) hash chain from its genesis and reports the first break.
// It is the operator / auditor side of the "export ↔ chain ↔ verify" loop:
// given only the exported rows (each carrying prev_hash / row_hash) and the
// anchor (genesis / tip / row_count), it independently re-derives every
// row_hash and confirms linkage, count, and tip — no database access required.
// Deliberately CLI-only; never exposed via MCP (spec § 7.3, hard rule #9).

// VerifyRow is one row to verify: its hash-covered Core plus the stored
// prev_hash / row_hash to check against the recomputation.
type VerifyRow struct {
	Core
	PrevHash []byte
	RowHash  []byte
}

// VerifyChainInput is one chain's worth of evidence, with rows ascending by id.
type VerifyChainInput struct {
	TeamID   string
	Month    time.Time
	Genesis  []byte
	Tip      []byte
	RowCount int64
	Rows     []VerifyRow
}

// ChainVerdict is the per-chain verification result.
type ChainVerdict struct {
	Month       string // YYYY-MM
	Rows        int    // alias kept for callers; mirrors RowCount
	RowCount    int
	OK          bool
	BreakReason string
	BreakID     int64
}

// VerifyChain recomputes the chain and returns the first detected break.
// Detection covers (spec § 7.1): single-row tamper (row_hash mismatch),
// deletion / insertion / reorder (linkage discontinuity), bulk deletion
// (row_count mismatch), and tail truncation / extension (tip mismatch).
func VerifyChain(in VerifyChainInput) ChainVerdict {
	v := ChainVerdict{
		Month:    in.Month.UTC().Format("2006-01"),
		Rows:     len(in.Rows),
		RowCount: len(in.Rows),
		OK:       true,
	}

	prev := in.Genesis
	for _, row := range in.Rows {
		if !bytes.Equal(row.PrevHash, prev) {
			return broke(v, row.ID, "linkage broken (prev_hash does not chain)")
		}
		canon, err := CanonicalCore(row.Core)
		if err != nil {
			return broke(v, row.ID, "canonicalisation failed: "+err.Error())
		}
		want := RowHash(prev, canon)
		if !bytes.Equal(row.RowHash, want) {
			return broke(v, row.ID, "row_hash mismatch (row tampered)")
		}
		prev = row.RowHash
	}

	if int64(len(in.Rows)) != in.RowCount {
		return broke(v, 0, "row_count mismatch (rows added or removed)")
	}
	if len(in.Tip) > 0 && !bytes.Equal(prev, in.Tip) {
		return broke(v, 0, "tip mismatch (chain truncated or extended)")
	}
	return v
}

func broke(v ChainVerdict, id int64, reason string) ChainVerdict {
	v.OK = false
	v.BreakID = id
	v.BreakReason = reason
	return v
}
