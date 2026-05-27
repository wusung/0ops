package migrationlint

import (
	"strings"
	"testing"
)

func TestRuleIndexConcurrentlyFlagsBareCreateIndex(t *testing.T) {
	src := []byte(`
-- +goose Up
CREATE INDEX foo_idx ON foo (bar);
`)
	v := ScanBytes("test.sql", src)
	if len(v) != 1 {
		t.Fatalf("want 1 violation, got %d (%v)", len(v), v)
	}
	if v[0].Rule != RuleIndexConcurrently {
		t.Errorf("want R1, got %s", v[0].Rule)
	}
}

func TestRuleIndexConcurrentlyFlagsAlterIndex(t *testing.T) {
	src := []byte(`ALTER INDEX foo_idx RENAME TO bar_idx;`)
	v := ScanBytes("test.sql", src)
	if len(v) != 1 || v[0].Rule != RuleIndexConcurrently {
		t.Fatalf("want one R1 violation, got %v", v)
	}
}

func TestRuleIndexConcurrentlyAcceptsConcurrentVariants(t *testing.T) {
	cases := []string{
		`CREATE INDEX CONCURRENTLY foo_idx ON foo (bar);`,
		`CREATE UNIQUE INDEX CONCURRENTLY foo_idx ON foo (bar);`,
		`create index concurrently if not exists foo_idx on foo (bar);`,
	}
	for _, c := range cases {
		v := ScanBytes("test.sql", []byte(c))
		if len(v) != 0 {
			t.Errorf("%q: want 0 violations, got %v", c, v)
		}
	}
}

func TestRuleIndexConcurrentlyIgnoresStatementsInsideComments(t *testing.T) {
	src := []byte(`
-- CREATE INDEX foo_idx ON foo (bar);  -- only documentation
/* CREATE INDEX bar_idx ON foo (bar); */
SELECT 1;
`)
	if v := ScanBytes("test.sql", src); len(v) != 0 {
		t.Errorf("comments must not trigger lint, got %v", v)
	}
}

func TestRuleIndexConcurrentlyIgnoresStringLiterals(t *testing.T) {
	src := []byte(`INSERT INTO audit_log (action) VALUES ('CREATE INDEX foo_idx ON foo (bar)');`)
	if v := ScanBytes("test.sql", src); len(v) != 0 {
		t.Errorf("string literals must not trigger lint, got %v", v)
	}
}

func TestRuleNotNullThreeStepFlagsCombinedAddColumn(t *testing.T) {
	src := []byte(`ALTER TABLE foo ADD COLUMN bar TEXT NOT NULL DEFAULT 'x';`)
	v := ScanBytes("test.sql", src)
	if len(v) != 1 || v[0].Rule != RuleNotNullThreeStep {
		t.Fatalf("want one R2 violation, got %v", v)
	}
}

func TestRuleNotNullThreeStepAcceptsNullableAdd(t *testing.T) {
	cases := []string{
		`ALTER TABLE foo ADD COLUMN bar TEXT;`,
		`ALTER TABLE foo ADD COLUMN bar TEXT DEFAULT 'x';`,
		`ALTER TABLE foo ADD COLUMN bar TEXT NOT NULL;`, // bare NOT NULL without DEFAULT is acceptable (small/empty table)
	}
	for _, c := range cases {
		v := ScanBytes("test.sql", []byte(c))
		if len(v) != 0 {
			t.Errorf("%q: want 0 violations, got %v", c, v)
		}
	}
}

func TestRuleNotNullThreeStepFlagsAcrossMultipleLines(t *testing.T) {
	src := []byte(`ALTER TABLE foo
    ADD COLUMN bar TEXT
        NOT NULL
        DEFAULT 'baseline';`)
	v := ScanBytes("test.sql", src)
	if len(v) != 1 || v[0].Rule != RuleNotNullThreeStep {
		t.Fatalf("want one R2 violation, got %v", v)
	}
}

func TestRuleNotNullThreeStepHandlesMultipleAddColumnsInOneStatement(t *testing.T) {
	src := []byte(`ALTER TABLE foo
    ADD COLUMN a TEXT,
    ADD COLUMN b TEXT NOT NULL DEFAULT 'x',
    ADD COLUMN c TEXT;`)
	v := ScanBytes("test.sql", src)
	if len(v) != 1 {
		t.Fatalf("want one R2 violation, got %v", v)
	}
	if !strings.Contains(v[0].Snippet, "b TEXT") {
		t.Errorf("want violation on column b, got %v", v[0])
	}
}

func TestAggregateNilOnEmpty(t *testing.T) {
	if err := Aggregate(nil); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestAggregateConcatenates(t *testing.T) {
	err := Aggregate([]Violation{
		{File: "a.sql", Line: 1, Rule: RuleIndexConcurrently, Reason: "x"},
		{File: "b.sql", Line: 2, Rule: RuleNotNullThreeStep, Reason: "y"},
	})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a.sql") || !strings.Contains(msg, "b.sql") {
		t.Errorf("aggregate must include both files, got %q", msg)
	}
}

func TestNumericPrefix(t *testing.T) {
	cases := map[string]int{
		"00001_init.sql":     1,
		"00007_audit.sql":    7,
		"5_no_padding.sql":   5,
		"no_prefix.sql":      0,
		"a01_alpha_lead.sql": 0,
	}
	for name, want := range cases {
		if got := numericPrefix(name); got != want {
			t.Errorf("numericPrefix(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestViolationStringIncludesRuleAndLine(t *testing.T) {
	v := Violation{File: "x.sql", Line: 42, Rule: RuleIndexConcurrently, Reason: "r", Snippet: "s"}
	s := v.String()
	if !strings.Contains(s, "x.sql:42") || !strings.Contains(s, string(RuleIndexConcurrently)) {
		t.Errorf("Violation.String missing context: %q", s)
	}
}
