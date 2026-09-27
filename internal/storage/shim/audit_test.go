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
)

// sqlMaskedRegions returns the single-quoted string literals (with
// their quotes) and the comment regions (`-- …`, `/* … */`) of one SQL
// text — everything the runtime rewriter's regexes do not see. The
// static audit checks the rule tokens against exactly these regions:
// a `datetime('now')` or `INSERT OR IGNORE` smuggled into a literal or
// comment would be invisible to the shim yet change the query's
// meaning.
func sqlMaskedRegions(s string) (literals, comments []string) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			end := scanQuoted(s, i, '\'')
			literals = append(literals, s[i:end])
			i = end - 1
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			rel := strings.IndexByte(s[i+2:], '\n')
			if rel < 0 {
				comments = append(comments, s[i:])
				i = len(s)
			} else {
				comments = append(comments, s[i:i+2+rel])
				i += 2 + rel
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			rel := strings.Index(s[i+2:], "*/")
			if rel < 0 {
				comments = append(comments, s[i+2:])
				i = len(s)
			} else {
				comments = append(comments, s[i+2:i+2+rel])
				i += rel + 3
			}
		}
	}
	return literals, comments
}

// auditRuleTokensRe checks the rewriter's blind spots: the exact token
// sequences its rules match on. A doubled quote inside a literal hides
// `datetime('now')` as `datetime(”now”)`, so literals are also
// probed in a de-doubled form.
var auditRuleTokens = []struct {
	name string
	re   *regexp.Regexp
}{
	{"datetime('now')", datetimeNowRe},
	{"INSERT OR IGNORE", insertOrIgnoreRe},
}

// TestStaticAudit_NoPlaceholderInsideSQLStringLiteral fails when any
// SQL literal in the storage layer hides a '?' inside quotes, or when
// a rewrite-rule token sits where the runtime rewriter cannot see it
// (inside a string literal or a comment).
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
			literals, comments := sqlMaskedRegions(s)
			for _, span := range literals {
				if strings.Contains(span, "?") {
					t.Errorf("%s: '?' inside SQL string literal %s (span %s)",
						fset.Position(lit.pos), name, span)
				}
			}
			for _, group := range [2][]string{literals, comments} {
				for _, span := range group {
					for _, tok := range auditRuleTokens {
						// Comments are probed as-is; literals also in
						// de-doubled form ('' collapses to ' inside a
						// literal's value space).
						for _, probe := range []string{span, strings.ReplaceAll(span, "''", "'")} {
							if tok.re.MatchString(probe) {
								t.Errorf("%s: %s sits where the runtime rewriter cannot see it (rule-token blind spot) in %s: %s",
									fset.Position(lit.pos), tok.name, name, span)
							}
						}
					}
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

// TestSqlMaskedRegions pins the blind-spot scanner on the reviewer's
// repro shapes: rule tokens hidden in literals and comments must be
// surfaced; clean SQL must stay clean.
func TestSqlMaskedRegions(t *testing.T) {
	tests := []struct {
		name             string
		sql              string
		wantLiteralHit   string // rule token expected inside a literal ("" = none)
		wantCommentHit   string // rule token expected inside a comment ("" = none)
		wantQuestionMark bool   // '?' expected inside a literal
	}{
		{
			name:           "reviewer repro: INSERT OR IGNORE inside a literal",
			sql:            `SELECT 'INSERT OR IGNORE' AS s FROM t`,
			wantLiteralHit: "INSERT OR IGNORE",
		},
		{
			name:           "doubled-quote smuggling of datetime('now')",
			sql:            "SELECT 'call datetime(''now'') again' AS s FROM t",
			wantLiteralHit: "datetime('now')",
		},
		{
			name:           "raw datetime('now') inside a literal",
			sql:            "SELECT 'stamp: datetime(''now'')' FROM t WHERE id = ?",
			wantLiteralHit: "datetime('now')",
		},
		{
			name:           "token inside a line comment",
			sql:            "INSERT INTO t (a) VALUES (?) -- historically INSERT OR IGNORE\n",
			wantCommentHit: "INSERT OR IGNORE",
		},
		{
			name:           "token inside a block comment",
			sql:            "SELECT /* datetime('now') legacy */ 1 FROM t",
			wantCommentHit: "datetime('now')",
		},
		{
			name:             "placeholder inside a literal",
			sql:              "SELECT * FROM t WHERE note = 'best ? ever' AND id = ?",
			wantQuestionMark: true,
		},
		{
			name: "clean query: no blind spots",
			sql:  "INSERT INTO task_tags (task_id, tag_id) VALUES (?, ?)",
		},
		{
			name: "code-position tokens are NOT blind spots",
			sql:  "INSERT OR IGNORE INTO s (a) VALUES (?, datetime('now'))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			literals, comments := sqlMaskedRegions(tt.sql)

			literalHit, commentHit := "", ""
			for _, span := range literals {
				for _, tok := range auditRuleTokens {
					if tok.re.MatchString(span) || tok.re.MatchString(strings.ReplaceAll(span, "''", "'")) {
						literalHit = tok.name
					}
				}
			}
			for _, span := range comments {
				for _, tok := range auditRuleTokens {
					if tok.re.MatchString(span) {
						commentHit = tok.name
					}
				}
			}
			assert.Equal(t, tt.wantLiteralHit, literalHit, "literal blind spot")
			assert.Equal(t, tt.wantCommentHit, commentHit, "comment blind spot")

			qInLiteral := false
			for _, span := range literals {
				if strings.Contains(span, "?") {
					qInLiteral = true
				}
			}
			assert.Equal(t, tt.wantQuestionMark, qInLiteral, "'?' inside literal")
		})
	}
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
