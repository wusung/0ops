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
// to UTC and truncated to microseconds to match Postgres timestamptz precision.
func CanonicalCore(c Core) ([]byte, error) {
	var b bytes.Buffer
	first := true
	write := func(s []byte) {
		if !first {
			b.WriteByte(coreFieldSep)
		}
		first = false
		b.Write(s)
	}
	enc := func(v any) ([]byte, error) { return json.Marshal(v) }

	idB, _ := enc(c.ID)
	write(idB)

	teamB, err := enc(c.TeamID)
	if err != nil {
		return nil, err
	}
	write(teamB)

	write(canonScalar(c.ActorUserID))
	srcB, _ := enc(c.Source)
	write(srcB)
	stB, _ := enc(c.SubjectType)
	write(stB)
	write(canonScalar(c.SubjectID))
	actB, _ := enc(c.Action)
	write(actB)

	argsB, err := canonicalJSON(c.Args)
	if err != nil {
		return nil, fmt.Errorf("audit: canonicalise args: %w", err)
	}
	write(argsB)
	resultB, err := canonicalJSON(c.Result)
	if err != nil {
		return nil, fmt.Errorf("audit: canonicalise result: %w", err)
	}
	write(resultB)

	write(canonScalar(c.PreviewID))
	traceB, _ := enc(c.TraceID)
	write(traceB)
	outB, _ := enc(c.Outcome)
	write(outB)
	write(canonHTTPStatus(c.HTTPStatus))

	tsB, _ := enc(c.CreatedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
	write(tsB)

	return b.Bytes(), nil
}

// canonScalar JSON-encodes a nullable string: nil → `null`, else a quoted,
// escaped string. This keeps a literal "null" string distinct from SQL NULL
// and an empty string distinct from both.
func canonScalar(p *string) []byte {
	if p == nil {
		return []byte("null")
	}
	out, _ := json.Marshal(*p)
	return out
}

func canonHTTPStatus(p *int) []byte {
	if p == nil {
		return []byte("null")
	}
	out, _ := json.Marshal(*p)
	return out
}

// canonicalJSON re-marshals raw JSON so semantically equal documents produce
// identical bytes: json.Marshal sorts map keys and strips insignificant
// whitespace, and we recurse implicitly via interface{}. Empty / nil raw
// (a row with no args) canonicalises to the literal `null`.
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
