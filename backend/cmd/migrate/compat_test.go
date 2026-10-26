package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Migrations run while the previous version of the API is still serving
// (docs/DEPLOY.md), so their Up section must not break it. These statements
// would; a statement that really needs one (the "contract" step, once no
// running version uses the old shape) carries a "-- compat: <why>" comment.
var breaking = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)\bDROP\s+(TABLE|COLUMN|TYPE|VIEW|FUNCTION)\b`), "drops something the running version may use"},
	{regexp.MustCompile(`(?i)\bRENAME\b`), "renames something the running version uses by its old name"},
	{regexp.MustCompile(`(?i)\bALTER\s+COLUMN\s+\S+\s+(SET\s+DATA\s+)?TYPE\b`), "changes a column type under the running version"},
	{regexp.MustCompile(`(?i)\bSET\s+NOT\s+NULL\b`), "the running version may still write NULLs"},
	{regexp.MustCompile(`(?i)\bADD\s+COLUMN\b[^,;]*\bNOT\s+NULL\b`), "a NOT NULL column without DEFAULT breaks the running version's INSERTs"},
}

func TestMigrationsAreBackwardCompatible(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		for _, stmt := range strings.Split(up, ";") {
			for _, problem := range breakingIn(stmt) {
				t.Errorf("%s: %s.\nSplit it into expand now and contract in a later release, or mark it \"-- compat: <why>\" (docs/DEPLOY.md).",
					filepath.Base(file), problem)
			}
		}
	}
}

// breakingIn lists what in one statement would break the running version.
func breakingIn(stmt string) []string {
	if strings.Contains(stmt, "-- compat:") {
		return nil
	}
	code := stripComments(stmt)
	var problems []string
	for _, b := range breaking {
		m := b.re.FindString(code)
		if m == "" {
			continue
		}
		// NOT NULL with a DEFAULT is fine: old INSERTs get the default.
		if strings.HasPrefix(strings.ToUpper(m), "ADD") && hasDefault.MatchString(addColumnClause(code, m)) {
			continue
		}
		problems = append(problems, strconv.Quote(strings.TrimSpace(m))+" "+b.why)
	}
	return problems
}

var hasDefault = regexp.MustCompile(`(?i)\bDEFAULT\b`)

func TestCompatCheckCatchesBreakingChanges(t *testing.T) {
	for stmt, want := range map[string]bool{
		"ALTER TABLE tasks DROP COLUMN due_date":                         true,
		"ALTER TABLE tasks RENAME COLUMN title TO name":                  true,
		"ALTER TABLE tasks ALTER COLUMN title TYPE varchar(80)":          true,
		"ALTER TABLE tasks ALTER COLUMN title SET NOT NULL":              true,
		"ALTER TABLE tasks ADD COLUMN size int NOT NULL":                 true,
		"ALTER TABLE tasks ADD COLUMN size int NOT NULL DEFAULT 0":       false,
		"ALTER TABLE tasks ADD COLUMN size int, ADD COLUMN x text":       false,
		"CREATE TABLE notes (id uuid PRIMARY KEY, body text NOT NULL)":   false,
		"-- drop column is only mentioned in a comment\nSELECT 1":        false,
		"ALTER TABLE tasks DROP COLUMN old -- compat: unused since v1.4": false,
	} {
		got := len(breakingIn(stmt)) > 0
		if got != want {
			t.Errorf("%q flagged = %v, want %v", stmt, got, want)
		}
	}
}

func stripComments(sql string) string {
	var out []string
	for line := range strings.SplitSeq(sql, "\n") {
		code, _, _ := strings.Cut(line, "--")
		out = append(out, code)
	}
	return strings.Join(out, "\n")
}

// addColumnClause is the ADD COLUMN clause that matched, up to the next comma
// or the end of the statement.
func addColumnClause(code, match string) string {
	i := strings.Index(code, match)
	rest := code[i:]
	if j := strings.IndexAny(rest, ",;"); j >= 0 {
		return rest[:j]
	}
	return rest
}
