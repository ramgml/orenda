package shim

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rebind scanner treats a '?' inside a single-quoted SQL literal as
// literal text and leaves it unbound. That is the right behaviour only
// as long as the storage corpus contains no such literals: a '?'
// confined to a literal would silently travel to the server while
// staying outside the $1..$n numbering. This static audit pins that
// invariant at the source level.
//
// It parses every non-test .go file in internal/storage/sqlite (the
// ~30 repository files whose SQL the shim carries), extracts the string
// literals that carry SQL and fails when a '?' appears inside a
// single-quoted span.
const auditSqliteDir = "../sqlite"

var (
	// auditSQLKeywordRe decides whether a Go string literal is SQL.
	// The vocabulary is deliberately narrow so ordinary error messages
	// and log lines don't trip the audit.
	auditSQLKeywordRe = regexp.MustCompile(`(?i)\b(select|insert|update|delete|create|values|returning|from|where|limit|join)\b`)
	// auditSQLLiteralRe yields the single-quoted spans of a SQL
	// literal (doubled '' reads as two adjacent spans).
	auditSQLLiteralRe = regexp.MustCompile(`'([^']*)'`)
)

// TestStaticAudit_NoPlaceholderInsideSQLStringLiteral fails when any
// SQL literal in the storage layer hides a '?' inside quotes.
func TestStaticAudit_NoPlaceholderInsideSQLStringLiteral(t *testing.T) {
	entries, err := os.ReadDir(auditSqliteDir)
	require.NoError(t, err, "audit must read the sqlite storage package")

	inspected := 0
	totalPlaceholders := 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(auditSqliteDir, name))
		require.NoError(t, err)

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err, "%s: audit must parse every repository file", name)

		for _, lit := range stringLiterals(file) {
			s, err := strconv.Unquote(lit.value)
			if err != nil {
				// Raw (backtick) literals never unquote; strip the
				// delimiters by hand — they carry SQL verbatim.
				s = strings.Trim(lit.value, "`")
			}
			if !auditSQLKeywordRe.MatchString(s) {
				continue
			}
			inspected++
			for _, m := range auditSQLLiteralRe.FindAllStringSubmatch(s, -1) {
				if strings.Contains(m[1], "?") {
					t.Errorf("%s: '?' inside SQL string literal %s (span %q)",
						fset.Position(lit.pos), name, m[0])
				}
			}
			totalPlaceholders += strings.Count(s, "?")
		}
	}

	// Guard against a vacuous audit: the corpus must actually have been
	// seen. The storage layer today carries ~600 positional markers in
	// several hundred SQL literals; the bounds below are set well below
	// that so unrelated refactors don't flip them, but far above zero
	// so an empty scan can't pass.
	assert.Greater(t, inspected, 50, "audit inspected too few SQL literals")
	assert.Greater(t, totalPlaceholders, 400, "audit saw too few '?' placeholders")
	t.Logf("audit: %d SQL literals inspected, %d positional '?' placeholders across internal/storage/sqlite",
		inspected, totalPlaceholders)
}

// litString is one Go string literal with its source position.
type litString struct {
	value string
	pos   token.Pos
}

// stringLiterals collects every interpreted or raw string literal in
// the file (comments and identifiers are not SQL carriers).
func stringLiterals(f *ast.File) []litString {
	var out []litString
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		out = append(out, litString{value: lit.Value, pos: lit.Pos()})
		return true
	})
	return out
}
