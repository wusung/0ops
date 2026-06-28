package cli

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// buildExportEnvelope produces a well-formed export envelope (manifest + entries
// with valid linkage hashes) the way the server would, so verifyEnvelope can be
// tested without a live backend.
func buildExportEnvelope(t *testing.T, n int) dto.AuditExportEnvelope {
	t.Helper()
	teamID := "11111111-1111-1111-1111-111111111111"
	month := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	genesis := audit.GenesisHash(teamID, month)

	prev := genesis
	entries := make([]dto.AuditExportEntry, 0, n)
	for i := 0; i < n; i++ {
		created := month.Add(time.Duration(i) * time.Minute)
		trace := "0af7651916cd43dd8448eb211c80319c"
		core := audit.Core{
			ID: int64(i + 1), TeamID: teamID, Source: "user", SubjectType: "app",
			Action: "create_app", Args: []byte("null"), Result: []byte("null"),
			TraceID: trace, Outcome: "success", CreatedAt: created,
		}
		canon, err := audit.CanonicalCore(core)
		if err != nil {
			t.Fatalf("canonicalise: %v", err)
		}
		rowHash := audit.RowHash(prev, canon)
		entries = append(entries, dto.AuditExportEntry{
			AuditLogEntry: dto.AuditLogEntry{
				ID: core.ID, Time: created, Source: "user", SubjectType: "app",
				Action: "create_app", Outcome: "success", TraceID: &trace,
			},
			PrevHash: hex.EncodeToString(prev),
			RowHash:  hex.EncodeToString(rowHash),
		})
		prev = rowHash
	}
	return dto.AuditExportEnvelope{
		Manifest: dto.IntegritySummary{
			TeamID: teamID, TeamSlug: "acme", RowCount: n,
			Chains: []dto.ChainSummary{{
				Month:       "2026-06",
				GenesisHash: hex.EncodeToString(genesis),
				TipHash:     hex.EncodeToString(prev),
				RowCount:    int64(n),
			}},
		},
		Entries: entries,
	}
}

func TestVerifyEnvelopeOK(t *testing.T) {
	report := verifyEnvelope(buildExportEnvelope(t, 3))
	if !report.OK {
		t.Fatalf("expected OK, got chains %+v", report.Chains)
	}
	if len(report.Chains) != 1 || report.Chains[0].RowCount != 3 {
		t.Fatalf("chains = %+v", report.Chains)
	}
}

func TestVerifyEnvelopeDetectsContentTamper(t *testing.T) {
	env := buildExportEnvelope(t, 3)
	// Tamper a stored field without recomputing the hash — the auditor's
	// recomputation must catch it.
	env.Entries[1].Action = "delete_app"
	report := verifyEnvelope(env)
	if report.OK {
		t.Fatal("expected verify to fail on content tamper")
	}
}

func TestVerifyEnvelopeDetectsTipTamper(t *testing.T) {
	env := buildExportEnvelope(t, 3)
	env.Manifest.Chains[0].TipHash = hex.EncodeToString([]byte{0xde, 0xad})
	report := verifyEnvelope(env)
	if report.OK {
		t.Fatal("expected verify to fail on tip mismatch")
	}
}
