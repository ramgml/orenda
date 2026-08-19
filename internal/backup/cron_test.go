package backup

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParse_ValidExpressions is the table-driven parser coverage.
// Each row exercises one syntactic shape: literal, list, range,
// step, wildcard, and the combined OR semantic for dom/dow. Failing
// rows here usually mean the field-ranges or step logic regressed
// rather than Next() — Next() is covered separately below.
func TestParse_ValidExpressions(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"daily-3am-utc", "0 3 * * *"},
		{"every-minute", "* * * * *"},
		{"hourly", "0 * * * *"},
		{"every-five-minutes", "*/5 * * * *"},
		{"range-within-hour", "0-30 * * * *"},
		{"list-of-minutes", "0,15,30,45 * * * *"},
		{"list-of-hours", "0 9,12,18 * * *"},
		{"dom-list", "0 0 1,15 * *"},
		{"month-list", "0 0 1 1,4,7,10 *"},
		{"dow-list-zero", "0 0 * * 0,6"},
		{"dow-list-seven-alias", "0 0 * * 7"}, // Sunday via 7
		{"feb-29", "0 0 29 2 *"},
		{"range-step", "0 9-17/2 * * *"},
		{"dom-with-step", "0 0 */5 * *"},
		{"dow-with-step", "0 0 * * */2"},
		{"single-everywhere", "30 12 15 6 3"},
		{"dom-and-dow-both-restricted", "0 0 1 * 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(tc.expr)
			require.NoError(t, err, "expr %q", tc.expr)
			assert.Equal(t, tc.expr, s.Expr(), "Expr() round-trips the original input")
		})
	}
}

// TestParse_InvalidExpressions pins every error path: bad field
// count, bad range, bad step, out-of-range value, malformed number.
// Each rejection should mention the field that failed so the PUT
// handler can surface a useful 400 message.
func TestParse_InvalidExpressions(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"empty", ""},
		{"one-field", "*"},
		{"six-fields", "* * * * * *"},
		{"minute-too-high", "60 * * * *"},
		{"minute-negative", "-1 * * * *"},
		{"hour-too-high", "0 24 * * *"},
		{"dom-zero", "0 0 0 * *"},
		{"dom-too-high", "0 0 32 * *"},
		{"month-zero", "0 0 1 0 *"},
		{"month-too-high", "0 0 1 13 *"},
		{"dow-too-high", "0 0 * * 8"},
		{"range-reversed", "30-15 * * * *"},
		{"step-zero", "*/0 * * * *"},
		{"step-negative", "*/-1 * * * *"},
		{"step-malformed", "*/abc * * * *"},
		{"value-malformed", "abc * * * *"},
		{"range-start-malformed", "abc-5 * * * *"},
		{"range-end-malformed", "5-abc * * * *"},
		{"step-empty-after-slash", "*/ * * * *"},
		{"range-empty-after-dash", "5- * * * *"},
		{"list-with-empty-part", "0,,30 * * * *"},
		{"range-overflow", "30 25-30 * * *"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.expr)
			require.Error(t, err, "expr %q should fail", tc.expr)
			// All error paths should mention "cron:" so the API
			// handler can prepend a useful prefix without losing
			// context. Pre-32.7 the PUT handler was happy to
			// accept any string here — pinning the prefix keeps
			// the surfaced message actionable.
			assert.True(t, strings.HasPrefix(err.Error(), "cron:") || strings.Contains(err.Error(), "cron:"),
				"error should mention cron, got %q", err.Error())
		})
	}
}

// TestSchedule_Next is the table-driven coverage for the
// next-fire calculation. Each row pins a real-world schedule to a
// concrete "from" time and expected "next" UTC time. The "DST
// neutral / UTC" DoD is exercised by every row (all math is in
// time.UTC); the "midnight" boundary gets explicit rows below.
func TestSchedule_Next(t *testing.T) {
	cases := []struct {
		name string
		expr string
		from time.Time
		want time.Time
	}{
		{
			name: "daily-3am-from-before",
			expr: "0 3 * * *",
			from: time.Date(2026, 8, 18, 11, 40, 0, 0, time.UTC),
			want: time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC),
		},
		{
			name: "daily-3am-from-after",
			expr: "0 3 * * *",
			from: time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC),
			want: time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC),
		},
		{
			name: "every-five-minutes",
			expr: "*/5 * * * *",
			from: time.Date(2026, 8, 18, 11, 41, 30, 0, time.UTC),
			want: time.Date(2026, 8, 18, 11, 45, 0, 0, time.UTC),
		},
		{
			name: "every-five-minutes-rolls-to-next-hour",
			expr: "*/5 * * * *",
			from: time.Date(2026, 8, 18, 11, 58, 30, 0, time.UTC),
			want: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "hourly-skips-ahead",
			expr: "0 * * * *",
			from: time.Date(2026, 8, 18, 11, 41, 0, 0, time.UTC),
			want: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "midnight-rolls-day",
			expr: "0 0 * * *",
			from: time.Date(2026, 8, 18, 23, 59, 30, 0, time.UTC),
			want: time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "month-rolls-year",
			expr: "0 0 1 1 *",
			from: time.Date(2026, 8, 18, 11, 40, 0, 0, time.UTC),
			want: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "feb-29-skips-to-next-leap",
			expr: "0 0 29 2 *",
			from: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			// 2026 is not a leap year; next leap is 2028.
			want: time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "feb-29-lands-on-leap-year",
			expr: "0 0 29 2 *",
			from: time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "dom-and-dow-both-restricted-OR-semantic",
			expr: "0 0 1 * 1",                                 // dom=1 OR dow=Monday
			from: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), // Saturday 2026-08-01
			// 2026-08-03 is Monday; first match after Saturday Aug 1 is Monday Aug 3.
			want: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "sunday-via-7",
			expr: "0 0 * * 7",
			from: time.Date(2026, 8, 18, 11, 40, 0, 0, time.UTC), // Tuesday
			// Next Sunday is 2026-08-23.
			want: time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "list-of-hours",
			expr: "0 9,12,18 * * *",
			from: time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC),
			want: time.Date(2026, 8, 18, 18, 0, 0, 0, time.UTC),
		},
		{
			name: "every-minute-returns-next-minute",
			expr: "* * * * *",
			from: time.Date(2026, 8, 18, 11, 40, 0, 0, time.UTC),
			want: time.Date(2026, 8, 18, 11, 41, 0, 0, time.UTC),
		},
		{
			name: "second-truncation-input",
			expr: "0 3 * * *",
			from: time.Date(2026, 8, 18, 11, 40, 33, 0, time.UTC),
			// Sub-minute input is truncated to the minute and bumped
			// past, so the answer is the next allowed fire from
			// 11:41 onwards, which is 03:00 the next day.
			want: time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC),
		},
		{
			name: "exact-match-input-returns-next",
			expr: "0 12 * * *",
			from: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
			// "from" is exactly on a fire — Next() must return the
			// following fire, not the input itself.
			want: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(tc.expr)
			require.NoError(t, err)
			got := s.Next(tc.from)
			assert.True(t, tc.want.Equal(got),
				"%s: from=%s expr=%q want=%s got=%s",
				tc.name, tc.from.Format(time.RFC3339), tc.expr,
				tc.want.Format(time.RFC3339), got.Format(time.RFC3339))
		})
	}
}

// TestSchedule_Next_DSTNeutral pins the DoD claim that cron math
// is DST-neutral (UTC). We pick two UTC instants that would map to
// different local times in a DST-observing zone but produce the same
// cron answer. The test runs against a fixed UTC location; the
// passing condition is that Next() returns the same UTC answer for
// a "from" time around the spring-forward boundary, regardless of
// what the host's local zone happens to be.
func TestSchedule_Next_DSTNeutral(t *testing.T) {
	// 2026-03-29 is the spring-forward day in Europe; we don't
	// rely on the host zone — we only verify the algorithm
	// produces the expected UTC fire time from a "from" inside the
	// DST window.
	s, err := Parse("30 1 * * *") // 01:30 UTC every day
	require.NoError(t, err)

	from := time.Date(2026, 3, 28, 22, 0, 0, 0, time.UTC)
	want := time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)
	got := s.Next(from)
	assert.True(t, want.Equal(got),
		"DST spring-forward: from=%s want=%s got=%s",
		from, want, got)

	// Fall-back: 2026-10-25 in Europe. Same expectation — fire
	// time is computed in UTC, so the host zone is irrelevant.
	from2 := time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC)
	want2 := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	got2 := s.Next(from2)
	assert.True(t, want2.Equal(got2),
		"DST fall-back: from=%s want=%s got=%s",
		from2, want2, got2)
}

// TestSchedule_Next_Unsatisfiable covers the "structurally
// unsatisfiable" branch: a schedule whose constraint set is empty
// within a 1-year window. We expect Next() to return the zero
// time so the scheduler can detect this and back off (it falls
// back to the daily default + logs via the notifier).
//
// Construction: dom=31 in February. February never has 31 days,
// so the walk burns through the whole year.
func TestSchedule_Next_Unsatisfiable(t *testing.T) {
	s, err := Parse("0 0 31 2 *")
	require.NoError(t, err)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := s.Next(from)
	assert.True(t, got.IsZero(),
		"Feb 31 should be unsatisfiable; got=%s", got)
}

// TestMustParse_DefaultSchedule pins the wire between
// DefaultSchedule and MustParse. If anyone edits DefaultSchedule
// to something that doesn't parse, the package fails to compile
// (MustParse panics). This is the cheap canary that keeps the
// scheduler's fallback path honest.
func TestMustParse_DefaultSchedule(t *testing.T) {
	s := MustParse(DefaultSchedule)
	assert.Equal(t, DefaultSchedule, s.Expr())
}

// TestSchedule_ExprRoundTrip ensures Expr() returns exactly what
// Parse() received. Handlers use Expr() to round-trip the value
// back to the UI; any silent normalisation would surprise the
// operator who just typed "* * * * *" and saw "every minute".
func TestSchedule_ExprRoundTrip(t *testing.T) {
	inputs := []string{
		"0 3 * * *",
		"*/5 * * * *",
		"0,15,30,45 9-17 * * *",
	}
	for _, in := range inputs {
		s, err := Parse(in)
		require.NoError(t, err)
		assert.Equal(t, in, s.Expr(), "Expr round-trip")
	}
}
