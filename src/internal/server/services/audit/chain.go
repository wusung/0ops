package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// Hash chain (audit-export-and-integrity spec § 4 / ADR-0015).
//
// Every audit_log row carries a row_hash that chains to the previous row in
// its (team_id, partition_month) chain, giving per-row tamper-evidence: any
// edit, delete, insert, or reorder breaks the chain and is detectable by
// recomputation (verify). The chain is keyed per (team, month) so that
// dropping a whole partition at 13 months never leaves a dangling prev_hash —
// the audit_chain_head anchor (never dropped) carries genesis/tip/row_count.
//
// Determinism is load-bearing: the hash is computed over the *redacted, as
// stored* projection of a row, with a canonical serialisation, so that the
// write-time hash, the export-time bytes, and the verify-time recomputation
// are byte-identical even after a round trip through jsonb (spec § 4.3).

const (
	// chainDomainSep namespaces genesis derivation and versions the scheme.
	// Bumping this string is a chain-format migration (spec § 12 Open issue).
	chainDomainSep = "0ops-audit-chain-v1"
	// coreFieldSep (ASCII unit separator) delimits Core fields so a value
	// containing the textual form of an adjacent field cannot forge a
	// boundary (spec § 4.3 rule 5).
	coreFieldSep = 0x1F
)

// Core is the deterministic, hash-covered projection of an audit_log row.
// Each field holds the value exactly as persisted (args / result already
// redacted + serialised). prev_hash and row_hash are deliberately excluded —
// they wrap Core, they are not part of it.
type Core struct {
	ID          int64
	TeamID      string
	ActorUserID *string
	Source      string
	SubjectType string
	SubjectID   *string
	Action      string
	Args        json.RawMessage
	Result      json.RawMessage
	PreviewID   *string
	TraceID     string
	Outcome     string
	HTTPStatus  *int
	CreatedAt   time.Time
}

// PartitionMonth normalises a row timestamp to the first instant of its UTC
// month. The (team_id, partition_month) tuple keys exactly one hash chain and
// matches the audit_log monthly partition boundary (migration 00007).
func PartitionMonth(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// GenesisHash derives a chain's genesis from its coordinates alone, so any
// verifier can recompute it without a stored secret (spec § 4.3). The genesis
// row's prev_hash equals this value.
func GenesisHash(teamID string, partitionMonth time.Time) []byte {
	h := sha256.New()
	h.Write([]byte(chainDomainSep))
	h.Write([]byte{coreFieldSep})
	h.Write([]byte(teamID))
	h.Write([]byte{coreFieldSep})
	h.Write([]byte(PartitionMonth(partitionMonth).Format("2006-01")))
	return h.Sum(nil)
}

// RowHash chains a row to its predecessor: SHA-256(prev_hash || canonical(core)).
func RowHash(prevHash, canonicalCore []byte) []byte {
	h := sha256.New()
	h.Write(prevHash)
	h.Write(canonicalCore)
	return h.Sum(nil)
}

// CanonicalCore renders Core to its deterministic byte form: each field in a
// fixed schema order, JSON-encoded so null / "" / "null" never collide, joined
// by the unit separator. args / result are re-canonicalised (recursive key
// sort) so jsonb key reordering cannot change the hash. created_at is pinned
// to UTC and truncated to microseconds.
//
// This byte recipe is the v1 wire contract: it is frozen so that a write-time
// hash, an exported row, and a verify-time recomputation are byte-identical,
// and so a third party can recompute it from the spec. Any change to field
// order, separators, the number normalisation in canonicalJSON, or the
// timestamp format silently invalidates every existing row_hash and MUST be
// shipped as a new scheme version (bump chainDomainSep). The golden-vector
// tests in chain_test.go pin the exact bytes to make such a change fail loudly.
func CanonicalCore(c Core) ([]byte, error) {
	var b bytes.Buffer
	first := true
	writeRaw := func(s []byte) {
		if !first {
			b.WriteByte(coreFieldSep)
		}
		first = false
		b.Write(s)
	}
	// json.Marshal of string / int / int64 never errors, so the error is safe
	// to drop. The encoding escapes every control byte: a 0x1F inside a value
	// becomes an escaped sequence, so no field value can forge a boundary.
	writeScalar := func(v any) { out, _ := json.Marshal(v); writeRaw(out) }

	writeScalar(c.ID)
	writeScalar(c.TeamID)
	writeRaw(canonNullable(c.ActorUserID))
	writeScalar(c.Source)
	writeScalar(c.SubjectType)
	writeRaw(canonNullable(c.SubjectID))
	writeScalar(c.Action)

	argsB, err := canonicalJSON(c.Args)
	if err != nil {
		return nil, fmt.Errorf("audit: canonicalise args: %w", err)
	}
	writeRaw(argsB)
	resultB, err := canonicalJSON(c.Result)
	if err != nil {
		return nil, fmt.Errorf("audit: canonicalise result: %w", err)
	}
	writeRaw(resultB)

	writeRaw(canonNullable(c.PreviewID))
	writeScalar(c.TraceID)
	writeScalar(c.Outcome)
	writeRaw(canonNullable(c.HTTPStatus))
	// Write-path invariant (enforced in the write-path slice, not here): the
	// row INSERTed into audit_log must carry exactly this truncated created_at,
	// because Postgres timestamptz rounds — not truncates — sub-microsecond
	// values. Hashing the truncated value while storing the raw value would make
	// verify recompute from a rounded timestamp and flag a legitimate row.
	writeScalar(c.CreatedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))

	return b.Bytes(), nil
}

// canonNullable JSON-encodes a nullable scalar: nil → `null`, else the marshalled
// value. This keeps SQL NULL, an empty string, and a literal "null" distinct.
func canonNullable[T any](p *T) []byte {
	if p == nil {
		return []byte("null")
	}
	out, _ := json.Marshal(*p)
	return out
}

// canonicalJSON re-marshals raw JSON so semantically equal documents produce
// identical bytes: json.Marshal sorts map keys (recursively, via interface{})
// and strips insignificant whitespace. Empty / nil raw (a row with no args)
// canonicalises to the literal `null`.
//
// v1 wire contract: numbers round-trip through float64 (the interface{} default),
// so integers beyond float64's exact range are normalised in the hash domain
// (the stored jsonb value is unaffected). Switching to decoder.UseNumber() would
// silently change every row_hash and is a scheme-version change, not a fix.
func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("null"), nil
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
