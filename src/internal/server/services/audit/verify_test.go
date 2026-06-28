package audit

import (
	"testing"
	"time"
)

// buildVerifyChain constructs a well-formed chain of n rows for a (team, month)
// and returns the input a verifier would reconstruct from an export.
func buildVerifyChain(t *testing.T, n int) VerifyChainInput {
	t.Helper()
	teamID := "11111111-1111-1111-1111-111111111111"
	month := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	genesis := GenesisHash(teamID, month)

	prev := genesis
	rows := make([]VerifyRow, 0, n)
	for i := 0; i < n; i++ {
		core := Core{
			ID: int64(i + 1), TeamID: teamID, Source: "user", SubjectType: "app",
			Action: "create_app", Args: []byte(`{"i":` + itoa(i) + `}`), Result: []byte(`null`),
			TraceID: "0af7651916cd43dd8448eb211c80319c", Outcome: "success",
			CreatedAt: month.Add(time.Duration(i) * time.Minute),
		}
		canon, err := CanonicalCore(core)
		if err != nil {
			t.Fatalf("canonicalise: %v", err)
		}
		rowHash := RowHash(prev, canon)
		rows = append(rows, VerifyRow{Core: core, PrevHash: prev, RowHash: rowHash})
		prev = rowHash
	}
	return VerifyChainInput{
		TeamID: teamID, Month: month, Genesis: genesis, Tip: prev,
		RowCount: int64(n), Rows: rows,
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestVerifyChainOK(t *testing.T) {
	in := buildVerifyChain(t, 3)
	v := VerifyChain(in)
	if !v.OK {
		t.Fatalf("expected OK chain, got break: %s at id=%d", v.BreakReason, v.BreakID)
	}
	if v.RowCount != 3 {
		t.Fatalf("row count = %d, want 3", v.RowCount)
	}
}

func TestVerifyChainDetectsRowHashTamper(t *testing.T) {
	in := buildVerifyChain(t, 3)
	// Tamper the stored row_hash of the middle row.
	in.Rows[1].RowHash = append([]byte(nil), in.Rows[1].RowHash...)
	in.Rows[1].RowHash[0] ^= 0xFF
	v := VerifyChain(in)
	if v.OK {
		t.Fatal("expected break on row_hash tamper")
	}
	if v.BreakID != in.Rows[1].ID {
		t.Fatalf("break id = %d, want %d", v.BreakID, in.Rows[1].ID)
	}
}

func TestVerifyChainDetectsContentTamper(t *testing.T) {
	in := buildVerifyChain(t, 3)
	// Tamper the stored content (action) without recomputing the hash.
	in.Rows[1].Action = "delete_app"
	v := VerifyChain(in)
	if v.OK {
		t.Fatal("expected break on content tamper (recomputed row_hash differs)")
	}
	if v.BreakID != in.Rows[1].ID {
		t.Fatalf("break id = %d, want %d", v.BreakID, in.Rows[1].ID)
	}
}

func TestVerifyChainDetectsLinkageBreak(t *testing.T) {
	in := buildVerifyChain(t, 3)
	// Simulate a deleted row: drop the middle row so row 3's prev_hash no longer
	// links to its predecessor and the count is short.
	in.Rows = []VerifyRow{in.Rows[0], in.Rows[2]}
	v := VerifyChain(in)
	if v.OK {
		t.Fatal("expected break on linkage discontinuity")
	}
}

func TestVerifyChainDetectsCountMismatch(t *testing.T) {
	in := buildVerifyChain(t, 3)
	in.RowCount = 5 // anchor says 5 but only 3 rows present
	v := VerifyChain(in)
	if v.OK {
		t.Fatal("expected break on row_count mismatch (rows removed)")
	}
}

func TestVerifyChainDetectsTipMismatch(t *testing.T) {
	in := buildVerifyChain(t, 3)
	in.Tip = append([]byte(nil), in.Tip...)
	in.Tip[0] ^= 0xFF
	v := VerifyChain(in)
	if v.OK {
		t.Fatal("expected break on tip mismatch (chain truncated/extended)")
	}
}
