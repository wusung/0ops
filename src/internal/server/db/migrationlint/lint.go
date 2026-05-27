// Package migrationlint enforces 0ops migration safety hard rules.
//
// Spec：docs/features/postgres-ha-and-dr/spec.md § 10.1 + § 16 hard rules #7 #8.
// Backed by ADR-0008 §4 / ADR-0009.
//
// Rules
//
//	R1  CREATE INDEX / ALTER INDEX statements must include CONCURRENTLY
//	    (rules out exclusive locks on production tables — hard rule #7).
//	R2  ADD COLUMN ... NOT NULL DEFAULT ... is forbidden (hard rule #8;
//	    must be split into nullable add → backfill → SET NOT NULL).
//
// Pre-existing migrations below the grandfathered floor are exempt. The
// floor is set at the migration filename prefix (numeric);  any migration
// authored at or above the floor is fully linted.
package migrationlint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultGrandfatherFloor is the numeric prefix at or above which new
// migrations are fully linted. Migrations with prefix below this floor are
// considered baseline and skipped. Bumped when a new migration is authored
// that complies with R1/R2; lowering is forbidden once raised.
//
// Current floor (M5.4):  9   — migrations 00001..00008 predate the lint.
const DefaultGrandfatherFloor = 9

// RuleID identifies a single migration safety rule.
type RuleID string

const (
	RuleIndexConcurrently RuleID = "R1_index_concurrently"
	RuleNotNullThreeStep  RuleID = "R2_not_null_three_step"
)

// Violation is a single rule failure inside a migration file.
type Violation struct {
	File    string
	Line    int
	Rule    RuleID
	Snippet string
	Reason  string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d [%s] %s — %s", v.File, v.Line, v.Rule, v.Reason, v.Snippet)
}

// ScanDir walks dir for .sql files and runs the lint rules against each.
// Files with a numeric prefix below floor are skipped (grandfathered).
//
// Returned violations are sorted by (file, line) for deterministic output.
func ScanDir(dir string, floor int) ([]Violation, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)

	var all []Violation
	for _, name := range files {
		prefix := numericPrefix(name)
		if prefix > 0 && prefix < floor {
			continue
		}
		full := filepath.Join(dir, name)
		data, err := os.ReadFile(filepath.Clean(full))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", full, err)
		}
		all = append(all, ScanBytes(name, data)...)
	}
	return all, nil
}

// ScanBytes is the file-content-only entry point used by both ScanDir and
// the unit tests. file is the display name; it is not opened.
func ScanBytes(file string, content []byte) []Violation {
	src := stripCommentsAndStrings(string(content))
	var v []Violation
	v = append(v, ruleIndexConcurrently(file, src)...)
	v = append(v, ruleNotNullThreeStep(file, src)...)
	sort.SliceStable(v, func(i, j int) bool {
		if v[i].File != v[j].File {
			return v[i].File < v[j].File
		}
		return v[i].Line < v[j].Line
	})
	return v
}

// Aggregate returns nil if no violations, or an error wrapping each one.
func Aggregate(vs []Violation) error {
	if len(vs) == 0 {
		return nil
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.String()
	}
	return errors.New(strings.Join(parts, "\n"))
}

// numericPrefix returns the leading integer in a goose migration filename
// (e.g. "00007_audit.sql" → 7). Returns 0 if no numeric prefix found.
func numericPrefix(name string) int {
	for i, r := range name {
		if r < '0' || r > '9' {
			if i == 0 {
				return 0
			}
			n, err := strconv.Atoi(name[:i])
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// stripCommentsAndStrings replaces SQL comments and single-quoted string
// literals with whitespace of equal length, preserving line numbering.
// This keeps regexes from matching inside narrative comments or escaped
// strings while letting the linter still report correct line numbers.
func stripCommentsAndStrings(s string) string {
	out := []byte(s)
	n := len(out)
	for i := 0; i < n; {
		// line comment
		if i+1 < n && out[i] == '-' && out[i+1] == '-' {
			j := i
			for j < n && out[j] != '\n' {
				out[j] = ' '
				j++
			}
			i = j
			continue
		}
		// block comment
		if i+1 < n && out[i] == '/' && out[i+1] == '*' {
			j := i
			for j+1 < n && !(out[j] == '*' && out[j+1] == '/') {
				if out[j] != '\n' {
					out[j] = ' '
				}
				j++
			}
			// blank out the closing */ too
			if j+1 < n {
				out[j] = ' '
				out[j+1] = ' '
				j += 2
			}
			i = j
			continue
		}
		// single-quoted literal (no escape handling needed — collapse contents)
		if out[i] == '\'' {
			j := i + 1
			for j < n && out[j] != '\'' {
				if out[j] != '\n' {
					out[j] = ' '
				}
				j++
			}
			if j < n {
				j++
			}
			out[i] = ' '
			if j-1 > i {
				out[j-1] = ' '
			}
			i = j
			continue
		}
		i++
	}
	return string(out)
}

var (
	reCreateIndex = regexp.MustCompile(`(?i)\bCREATE(\s+UNIQUE)?\s+INDEX\b`)
	reAlterIndex  = regexp.MustCompile(`(?i)\bALTER\s+INDEX\b`)
	reConcurrent  = regexp.MustCompile(`(?i)\bCONCURRENTLY\b`)

	// Match an ADD COLUMN clause and the rest of the clause until ',' or ';'.
	// (?s) lets . match newlines so we can carry NOT NULL / DEFAULT split
	// across lines.
	reAddColumn = regexp.MustCompile(`(?is)\bADD\s+COLUMN(?:\s+IF\s+NOT\s+EXISTS)?\b[^,;]*`)
)

func ruleIndexConcurrently(file, src string) []Violation {
	var out []Violation
	for _, m := range reCreateIndex.FindAllStringIndex(src, -1) {
		stmt := statementEndingAt(src, m[0])
		if !reConcurrent.MatchString(stmt) {
			out = append(out, Violation{
				File:    file,
				Line:    lineOf(src, m[0]),
				Rule:    RuleIndexConcurrently,
				Snippet: firstLine(stmt),
				Reason:  "CREATE INDEX without CONCURRENTLY (spec § 16 hard rule #7)",
			})
		}
	}
	for _, m := range reAlterIndex.FindAllStringIndex(src, -1) {
		stmt := statementEndingAt(src, m[0])
		if !reConcurrent.MatchString(stmt) {
			out = append(out, Violation{
				File:    file,
				Line:    lineOf(src, m[0]),
				Rule:    RuleIndexConcurrently,
				Snippet: firstLine(stmt),
				Reason:  "ALTER INDEX without CONCURRENTLY (spec § 16 hard rule #7)",
			})
		}
	}
	return out
}

func ruleNotNullThreeStep(file, src string) []Violation {
	var out []Violation
	for _, m := range reAddColumn.FindAllStringIndex(src, -1) {
		clause := src[m[0]:m[1]]
		hasNotNull := regexp.MustCompile(`(?i)\bNOT\s+NULL\b`).MatchString(clause)
		hasDefault := regexp.MustCompile(`(?i)\bDEFAULT\b`).MatchString(clause)
		if hasNotNull && hasDefault {
			out = append(out, Violation{
				File:    file,
				Line:    lineOf(src, m[0]),
				Rule:    RuleNotNullThreeStep,
				Snippet: firstLine(clause),
				Reason:  "ADD COLUMN NOT NULL DEFAULT must be split into 3 steps (spec § 16 hard rule #8)",
			})
		}
	}
	return out
}

// statementEndingAt returns the slice from start until the next ';'.
func statementEndingAt(src string, start int) string {
	end := strings.IndexByte(src[start:], ';')
	if end < 0 {
		return src[start:]
	}
	return src[start : start+end+1]
}

func lineOf(src string, idx int) int {
	return strings.Count(src[:idx], "\n") + 1
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
