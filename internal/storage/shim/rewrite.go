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
	return rebindPlaceholders(q), nil
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
