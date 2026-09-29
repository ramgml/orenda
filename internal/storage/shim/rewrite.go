package shim

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// rewriteFor rewrites one SQL statement from the SQLite dialect to the
// target dialect. DialectSQLite is the identity by contract — the
// sqlite path is pinned with byte-exact pass-through assertions in the
// tests.
func rewriteFor(target Dialect, q string) (string, error) {
	if target != DialectPostgres {
		return q, nil
	}
	q = rewriteDatetimeNow(q)
	q, err := rewriteInsertOrIgnore(q)
	if err != nil {
		return "", err
	}
	return rebindPlaceholders(q), nil
}

// datetimeNowRe matches SQLite's UTC now-stamp exactly as the storage
// layer writes it: datetime('now'), modulo internal whitespace. The
// pattern is case-sensitive on purpose — 'now' is a string value for
// SQLite (datetime('NOW') yields NULL), so only the strict form may be
// rewritten, and argument variants like datetime('now','localtime')
// are different functions with different semantics. The static corpus
// audit keeps every call site on exactly this shape.
var datetimeNowRe = regexp.MustCompile(`\bdatetime\s*\(\s*'now'\s*\)`)

// pgNowUTC renders the same value SQLite's datetime('now') emits: the
// current UTC time as a fixed-width, zero-padded, 19-character
// "YYYY-MM-DD HH24:MI:SS" TEXT stamp. Keeping the layout identical
// preserves the schema's lexicographic timestamp comparisons: for this
// layout, string order equals chronological order, so every
// created_at <= datetime('now')-style predicate keeps its meaning.
const pgNowUTC = `to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS')`

// rewriteDatetimeNow replaces every strict-form datetime('now') with
// the PostgreSQL equivalent.
func rewriteDatetimeNow(q string) string {
	return datetimeNowRe.ReplaceAllLiteralString(q, pgNowUTC)
}

// insertOrIgnoreRe matches SQLite's conflict-skipping INSERT prefix.
// Case-insensitive to survive hand-written SQL, strict on the OR
// IGNORE token pair so MySQL-style INSERT IGNORE or plain INSERTs are
// never touched.
var insertOrIgnoreRe = regexp.MustCompile(`(?i)\bINSERT\s+OR\s+IGNORE\b`)

// rewriteInsertOrIgnore rewrites INSERT OR IGNORE INTO into its
// PostgreSQL equivalent: the marker is dropped and the statement ends
// with ON CONFLICT DO NOTHING. The clause is statement-final in
// PostgreSQL, so it is appended after trailing whitespace and one
// trailing semicolon are trimmed — the storage layer never batches
// statements through the shim (the migration runner works directly on
// the sqlite driver and never enters this path). A query carrying more
// than one INSERT statement, or one whose tail lies inside a comment,
// is rejected loudly instead of mangled.
func rewriteInsertOrIgnore(q string) (string, error) {
	locs := insertOrIgnoreRe.FindAllStringIndex(q, -1)
	if len(locs) == 0 {
		return q, nil
	}
	if len(locs) > 1 {
		return "", fmt.Errorf("shim: unsupported query shape: %d INSERT OR IGNORE statements in one query", len(locs))
	}
	loc := locs[0]
	q = q[:loc[0]] + "INSERT" + q[loc[1]:]
	q = strings.TrimRight(q, " \t\r\n;")
	if endsInsideComment(q) {
		return "", fmt.Errorf("shim: unsupported query shape: statement ends inside a comment; the appended ON CONFLICT DO NOTHING clause would be swallowed")
	}
	return q + " ON CONFLICT DO NOTHING", nil
}

// endsInsideComment reports whether q ends inside an open comment
// region: a `--` line comment whose newline was trimmed away (or never
// existed), or an unterminated `/*` block. Appending a statement-final
// clause there would silently move it into the comment.
func endsInsideComment(q string) bool {
	for i := 0; i < len(q); i++ {
		switch c := q[i]; {
		case c == '\'':
			i = scanQuoted(q, i, '\'') - 1
		case c == '"':
			i = scanQuoted(q, i, '"') - 1
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			rel := strings.IndexByte(q[i+2:], '\n')
			if rel < 0 {
				return true
			}
			i += rel + 1 // resume after the closing newline
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			rel := strings.Index(q[i+2:], "*/")
			if rel < 0 {
				return true
			}
			i += rel + 3
		}
	}
	return false
}

// rewrite applies the full PostgreSQL rule set to one statement.
func rewrite(q string) (string, error) {
	return rewriteFor(DialectPostgres, q)
}

// rebindPlaceholders renumbers SQLite's anonymous ? markers to
// PostgreSQL's $1..$n. Markers inside single-quoted string literals,
// double-quoted identifiers and comments are left alone — a '?' there
// is SQL text, not a parameter (see the unit test documenting the
// behaviour and the static audit that keeps the storage corpus free of
// such literals). SQLite's named-parameter syntax (@x, :x, $x) is not
// used anywhere in the storage layer and is not rewritten.
func rebindPlaceholders(q string) string {
	var (
		b strings.Builder
		n int
		i int
	)
	b.Grow(len(q) + 8)
	for i < len(q) {
		switch c := q[i]; {
		case c == '\'':
			end := scanQuoted(q, i, '\'')
			b.WriteString(q[i:end])
			i = end
		case c == '"':
			end := scanQuoted(q, i, '"')
			b.WriteString(q[i:end])
			i = end
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			end := strings.IndexByte(q[i:], '\n')
			if end < 0 {
				end = len(q)
			} else {
				end += i + 1 // include the newline
			}
			b.WriteString(q[i:end])
			i = end
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			rel := strings.Index(q[i+2:], "*/")
			if rel < 0 {
				b.WriteString(q[i:])
				i = len(q)
			} else {
				end := i + 2 + rel + 2
				b.WriteString(q[i:end])
				i = end
			}
		case c == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// scanQuoted returns the index just past the quoted region that starts
// at start (q[start] holds the quote character). SQL doubles the quote
// character to escape it (” inside strings, "" inside identifiers);
// an unterminated region consumes the rest of the query.
func scanQuoted(q string, start int, quote byte) int {
	for i := start + 1; i < len(q); i++ {
		if q[i] != quote {
			continue
		}
		if i+1 < len(q) && q[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}
	return len(q)
}

// placeholderRegex is the inverse view of rebindPlaceholders: the
// $n markers it produced. Tests use it to assert the numbering.
var placeholderRegex = regexp.MustCompile(`\$(\d+)`)

// placeholderNumbers lists the placeholder ordinals in q in textual
// order. Test helper.
func placeholderNumbers(q string) []int {
	matches := placeholderRegex.FindAllStringSubmatch(q, -1)
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			panic(fmt.Sprintf("shim test: bad placeholder %q: %v", m[0], err))
		}
		out = append(out, v)
	}
	return out
}
