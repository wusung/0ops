package audit

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

func baseCore() Core {
	return Core{
		ID:          42,
		TeamID:      "11111111-1111-1111-1111-111111111111",
		ActorUserID: strptr("22222222-2222-2222-2222-222222222222"),
		Source:      "user",
		SubjectType: "app",
		SubjectID:   strptr("33333333-3333-3333-3333-333333333333"),
		Action:      "create_app",
		Args:        json.RawMessage(`{"slug":"demo","ref":"main"}`),
		Result:      json.RawMessage(`{"ok":true}`),
		PreviewID:   strptr("44444444-4444-4444-4444-444444444444"),
		TraceID:     "0af7651916cd43dd8448eb211c80319c",
		Outcome:     "success",
		HTTPStatus:  intptr(200),
		CreatedAt:   time.Date(2026, 6, 28, 12, 34, 56, 123456789, time.UTC),
	}
}

func mustCanon(t *testing.T, c Core) []byte {
	t.Helper()
	b, err := CanonicalCore(c)
	if err != nil {
		t.Fatalf("CanonicalCore: %v", err)
	}
	return b
}

func TestPartitionMonth(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 6, 28, 23, 59, 59, 0, time.UTC), "2026-06-01"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01-01"},
		// A timestamp in a +08:00 zone late on the 31st is still July in UTC.
		{time.Date(2026, 8, 1, 5, 0, 0, 0, time.FixedZone("CST", 8*3600)), "2026-07-01"},
	}
	for _, c := range cases {
		got := PartitionMonth(c.in).Format("2006-01-02")
		if got != c.want {
			t.Errorf("PartitionMonth(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestGenesisHashStableAndCoordinateBound(t *testing.T) {
	team := "11111111-1111-1111-1111-111111111111"
	m := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	g1 := GenesisHash(team, m)
	g2 := GenesisHash(team, time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)) // same month
	if !bytes.Equal(g1, g2) {
		t.Fatal("genesis must depend only on (team, month), not day/time")
	}
	if len(g1) != 32 {
		t.Fatalf("genesis hash len = %d, want 32 (sha-256)", len(g1))
	}

	if bytes.Equal(g1, GenesisHash(team, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))) {
		t.Error("different month must yield different genesis")
	}
	if bytes.Equal(g1, GenesisHash("99999999-9999-9999-9999-999999999999", m)) {
		t.Error("different team must yield different genesis")
	}
}

func TestCanonicalCoreDeterministic(t *testing.T) {
	c := baseCore()
	if !bytes.Equal(mustCanon(t, c), mustCanon(t, c)) {
		t.Fatal("CanonicalCore not deterministic across calls")
	}
}

func TestCanonicalCoreArgsKeyOrderIndependent(t *testing.T) {
	a := baseCore()
	a.Args = json.RawMessage(`{"slug":"demo","ref":"main"}`)
	b := baseCore()
	b.Args = json.RawMessage(`{"ref":"main","slug":"demo"}`) // reordered keys + would differ raw
	if !bytes.Equal(mustCanon(t, a), mustCanon(t, b)) {
		t.Fatal("canonicalisation must be insensitive to JSON key order (jsonb round-trip safety)")
	}
}

func TestCanonicalCoreWhitespaceInsensitive(t *testing.T) {
	a := baseCore()
	a.Result = json.RawMessage(`{"ok":true}`)
	b := baseCore()
	b.Result = json.RawMessage("{ \"ok\" : true }")
	if !bytes.Equal(mustCanon(t, a), mustCanon(t, b)) {
		t.Fatal("canonicalisation must ignore insignificant whitespace")
	}
}

func TestCanonicalCoreNullVsEmptyVsLiteral(t *testing.T) {
	nullSubj := baseCore()
	nullSubj.SubjectID = nil
	emptySubj := baseCore()
	emptySubj.SubjectID = strptr("")
	literalNull := baseCore()
	literalNull.SubjectID = strptr("null")

	a := mustCanon(t, nullSubj)
	b := mustCanon(t, emptySubj)
	c := mustCanon(t, literalNull)
	if bytes.Equal(a, b) || bytes.Equal(a, c) || bytes.Equal(b, c) {
		t.Fatal("SQL NULL, empty string, and literal \"null\" must canonicalise distinctly")
	}
}

func TestCanonicalCoreCreatedAtMicrosecondTruncation(t *testing.T) {
	a := baseCore()
	a.CreatedAt = time.Date(2026, 6, 28, 12, 0, 0, 123456000, time.UTC)
	b := baseCore()
	b.CreatedAt = time.Date(2026, 6, 28, 12, 0, 0, 123456999, time.UTC) // same microsecond
	if !bytes.Equal(mustCanon(t, a), mustCanon(t, b)) {
		t.Fatal("sub-microsecond nanos must truncate to the same canonical form")
	}
	c := baseCore()
	c.CreatedAt = time.Date(2026, 6, 28, 12, 0, 0, 123457000, time.UTC) // next microsecond
	if bytes.Equal(mustCanon(t, a), mustCanon(t, c)) {
		t.Fatal("a different microsecond must change the canonical form")
	}
}

func TestCanonicalCoreCreatedAtZoneNormalised(t *testing.T) {
	utc := baseCore()
	utc.CreatedAt = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	plus8 := baseCore()
	plus8.CreatedAt = time.Date(2026, 6, 28, 20, 0, 0, 0, time.FixedZone("CST", 8*3600)) // same instant
	if !bytes.Equal(mustCanon(t, utc), mustCanon(t, plus8)) {
		t.Fatal("equal instants in different zones must canonicalise identically")
	}
}

func TestRowHashLinkageAndTamperDetection(t *testing.T) {
	c := baseCore()
	core := mustCanon(t, c)
	prev := GenesisHash(c.TeamID, c.CreatedAt)

	h := RowHash(prev, core)
	if len(h) != 32 {
		t.Fatalf("row hash len = %d, want 32", len(h))
	}
	// Same inputs → same hash.
	if !bytes.Equal(h, RowHash(prev, core)) {
		t.Fatal("RowHash not deterministic")
	}
	// Different predecessor → different hash (chain linkage).
	if bytes.Equal(h, RowHash(GenesisHash("other", c.CreatedAt), core)) {
		t.Fatal("changing prev_hash must change row_hash")
	}

	// Tampering with any covered field must change the hash.
	mutate := []func(*Core){
		func(x *Core) { x.ID = 43 },
		func(x *Core) { x.Action = "delete_app" },
		func(x *Core) { x.Outcome = "failure" },
		func(x *Core) { x.Args = json.RawMessage(`{"slug":"hacked","ref":"main"}`) },
		func(x *Core) { x.Result = json.RawMessage(`{"ok":false}`) },
		func(x *Core) { x.ActorUserID = nil },
		func(x *Core) { x.HTTPStatus = intptr(500) },
		func(x *Core) { x.TraceID = "ffffffffffffffffffffffffffffffff" },
	}
	for i, m := range mutate {
		tampered := baseCore()
		m(&tampered)
		if bytes.Equal(h, RowHash(prev, mustCanon(t, tampered))) {
			t.Errorf("mutation %d: tampered row produced identical hash", i)
		}
	}
}

// goldenCore is a fully-pinned row used by the known-answer test below.
func goldenCore() Core {
	return Core{
		ID:          1,
		TeamID:      "team-golden-0001",
		ActorUserID: strptr("actor-golden-0001"),
		Source:      "user",
		SubjectType: "app",
		SubjectID:   nil,
		Action:      "create_app",
		Args:        json.RawMessage(`{"slug":"demo"}`),
		Result:      json.RawMessage(`null`),
		PreviewID:   nil,
		TraceID:     "00000000000000000000000000000001",
		Outcome:     "success",
		HTTPStatus:  nil,
		CreatedAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestGoldenVectors pins the exact bytes of the v1 chain format. Any change to
// field order, the separator, number/timestamp normalisation, or genesis
// derivation breaks these constants — that is the point. A failure here means
// the wire contract changed and chainDomainSep must be bumped (spec § 12),
// existing row_hash values are invalidated, and third-party verifiers relying
// on the documented recipe must be updated.
func TestGoldenVectors(t *testing.T) {
	const (
		wantGenesis = "aa178e44465db669a20060939c06fe900357677754cd6f96dfc5f01fda2e5037"
		wantRowHash = "ef3b5f0bbeda39719ad5bacab83b63d164973bfd44971a3ed248e010da61766c"
	)
	genesis := GenesisHash("team-golden-0001", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if got := hex.EncodeToString(genesis); got != wantGenesis {
		t.Errorf("GenesisHash golden mismatch:\n got  %s\n want %s", got, wantGenesis)
	}
	core := mustCanon(t, goldenCore())
	if got := hex.EncodeToString(RowHash(genesis, core)); got != wantRowHash {
		t.Errorf("RowHash golden mismatch:\n got  %s\n want %s", got, wantRowHash)
	}
}

// TestCanonicalCoreNestedKeyOrderIndependent guards spec § 4.3 rule 1: key
// sorting must recurse into nested objects, not just the top level.
func TestCanonicalCoreNestedKeyOrderIndependent(t *testing.T) {
	a := baseCore()
	a.Args = json.RawMessage(`{"outer":{"a":1,"b":2},"z":3}`)
	b := baseCore()
	b.Args = json.RawMessage(`{"z":3,"outer":{"b":2,"a":1}}`)
	if !bytes.Equal(mustCanon(t, a), mustCanon(t, b)) {
		t.Fatal("nested object key reordering must not change the canonical form")
	}
}

// TestCanonicalCoreSeparatorInjection turns the 0x1F boundary defence into a
// regression: a value containing a literal separator byte, and a "boundary
// shift" between two adjacent fields, must both canonicalise distinctly from
// the forged alternative.
func TestCanonicalCoreSeparatorInjection(t *testing.T) {
	// A literal 0x1F inside a value must not collapse into the real separator.
	withSep := baseCore()
	withSep.Action = "a\x1fb"
	plain := baseCore()
	plain.Action = "ab"
	if bytes.Equal(mustCanon(t, withSep), mustCanon(t, plain)) {
		t.Fatal("a value containing 0x1F must not canonicalise like the same value without it")
	}

	// Boundary shift: (subject_type="x\x1fy", action="...") must differ from
	// (subject_type="x", action that begins "y..."). The fixed schema order
	// plus escaping makes forgery impossible; assert it.
	shifted := baseCore()
	shifted.SubjectType = "x\x1fy"
	shifted.Action = "zzz"
	forged := baseCore()
	forged.SubjectType = "x"
	forged.Action = "y\x1fzzz"
	if bytes.Equal(mustCanon(t, shifted), mustCanon(t, forged)) {
		t.Fatal("field boundaries must not be forgeable by embedding separators")
	}
}

func TestCanonicalCoreNilArgsResult(t *testing.T) {
	c := baseCore()
	c.Args = nil
	c.Result = nil
	// nil args/result must canonicalise (to literal null) without error and
	// match an explicit JSON null.
	a := mustCanon(t, c)
	c2 := baseCore()
	c2.Args = json.RawMessage("null")
	c2.Result = json.RawMessage(`null`)
	if !bytes.Equal(a, mustCanon(t, c2)) {
		t.Fatal("nil and literal null args/result must canonicalise identically")
	}
}
