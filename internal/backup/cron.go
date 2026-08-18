// Package backup — cron expression parser + Schedule calculator.
//
// Phase 32.7 backs the snapshot fire time in the scheduler with a
// 5-field cron expression that the operator edits from
// /settings/backups. We own a minimal parser here (UTC only,
// minute-hour-dom-month-dow) instead of pulling robfig/cron:
//
//   - The schedule is bounded — minute, hour, day-of-month, month,
//     day-of-week. No seconds field, no @yearly/@hourly macros, no
//     timezone support (operators convert by hand if they need a
//     local TZ).
//   - All math is in UTC, which makes the schedule DST-neutral by
//     construction (no "fire skipped" / "fire twice" surprises on
//     clock changes).
//   - Zero new dependency — robfig/cron is BSD-3-Clause but adds
//     200 KB and a transitive surface that the bounded parser
//     doesn't need. AGENTS.md forbids new dependencies without
//     justification; this task explicitly called out the
//     "own-vs-robfig" decision and the parser below is the
//     justification (≈200 lines, fully covered by the table-driven
//     tests in cron_test.go).
//
// The parser handles the standard cron field shapes:
//
//   - "*"            every value in the field's range
//   - "n"            specific value
//   - "n-m"          inclusive range
//   - "*/k"          every k-th value, starting at the field min
//   - "n-m/k"        every k-th value within [n, m]
//   - "a,b,c"        comma-separated list of any of the above
//
// Day-of-month and day-of-week follow cron's OR semantic: when
// *both* are restricted (i.e. neither is "*"), a date matches when
// *either* hits. If only one is restricted, only that one matters.
// Day-of-week accepts 0 and 7 as Sunday (cron convention); 7 is
// folded to 0 during parse so both forms produce the same Schedule.
package backup

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Field ranges, matching classic cron.
const (
	minMinute = 0
	maxMinute = 59
	minHour   = 0
	maxHour   = 23
	minDOM    = 1
	maxDOM    = 31
	minMonth  = 1
	maxMonth  = 12
	minDOW    = 0
	maxDOW    = 6 // cron convention: Sunday = 0 (and 7)
)

// cronField is a set of allowed values within a single cron field.
// bits has bit v set when value v is allowed. 64 bits is enough for
// any single cron field (the largest is 0..59 = 60 values).
type cronField struct {
	bits uint64
}

func (f cronField) contains(v int) bool {
	return v >= 0 && v < 64 && f.bits&(1<<uint(v)) != 0
}

// Schedule is a parsed 5-field cron expression, ready to compute
// the next fire time in UTC.
type Schedule struct {
	minute, hour, dom, month, dow cronField
	// domRestricted / dowRestricted track the original expression
	// shape ("*" vs not-"*"). They implement cron's OR semantic for
	// the day-of-month / day-of-week combination — see Next().
	domRestricted, dowRestricted bool
	expr                         string
}

// Expr returns the original cron expression the Schedule was parsed
// from. Used in logs and to round-trip the value through APIs.
func (s Schedule) Expr() string { return s.expr }

// Parse parses a 5-field cron expression. The returned Schedule is
// ready for Next() calls. Returns an error for any syntax problem
// (wrong field count, out-of-range value, malformed step/range).
//
// Day-of-week: 0 and 7 both mean Sunday (cron convention). We fold
// 7 → 0 during parse so callers can use either form.
func Parse(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}
	minute, err := parseCronField(fields[0], minMinute, maxMinute)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: minute: %w", err)
	}
	hour, err := parseCronField(fields[1], minHour, maxHour)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: hour: %w", err)
	}
	dom, err := parseCronField(fields[2], minDOM, maxDOM)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: day-of-month: %w", err)
	}
	month, err := parseCronField(fields[3], minMonth, maxMonth)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: month: %w", err)
	}
	// Day-of-week: accept 7 as Sunday alias (cron convention).
	dowExpr := strings.ReplaceAll(fields[4], "7", "0")
	dow, err := parseCronField(dowExpr, minDOW, maxDOW)
	if err != nil {
		return Schedule{}, fmt.Errorf("cron: day-of-week: %w", err)
	}
	return Schedule{
		minute:        minute,
		hour:          hour,
		dom:           dom,
		month:         month,
		dow:           dow,
		domRestricted: fields[2] != "*",
		dowRestricted: fields[4] != "*",
		expr:          expr,
	}, nil
}

// parseCronField parses one comma-separated cron field with an
// optional "/step" suffix. The range is clamped to [minVal, maxVal].
// Out-of-range values, malformed steps, and reversed ranges are
// rejected.
func parseCronField(s string, minVal, maxVal int) (cronField, error) {
	var f cronField
	for _, part := range strings.Split(s, ",") {
		step := 1
		if idx := strings.Index(part, "/"); idx >= 0 {
			stepStr := part[idx+1:]
			if stepStr == "" {
				return cronField{}, fmt.Errorf("empty step in %q", part)
			}
			n, err := strconv.Atoi(stepStr)
			if err != nil {
				return cronField{}, fmt.Errorf("invalid step %q: %w", stepStr, err)
			}
			if n <= 0 {
				return cronField{}, fmt.Errorf("step must be positive, got %d", n)
			}
			step = n
			part = part[:idx]
		}
		if part == "" {
			return cronField{}, fmt.Errorf("empty range in %q", s)
		}
		var start, end int
		switch {
		case part == "*":
			start, end = minVal, maxVal
		case strings.Contains(part, "-"):
			idx := strings.Index(part, "-")
			a, err := strconv.Atoi(part[:idx])
			if err != nil {
				return cronField{}, fmt.Errorf("invalid range start in %q: %w", part, err)
			}
			b, err := strconv.Atoi(part[idx+1:])
			if err != nil {
				return cronField{}, fmt.Errorf("invalid range end in %q: %w", part, err)
			}
			if a > b {
				return cronField{}, fmt.Errorf("range start > end in %q", part)
			}
			start, end = a, b
		default:
			v, err := strconv.Atoi(part)
			if err != nil {
				return cronField{}, fmt.Errorf("invalid value %q: %w", part, err)
			}
			start, end = v, v
		}
		if start < minVal || end > maxVal {
			return cronField{}, fmt.Errorf("value out of range [%d, %d] in %q", minVal, maxVal, s)
		}
		for v := start; v <= end; v += step {
			f.bits |= 1 << uint(v)
		}
	}
	return f, nil
}

// DefaultSchedule is the cron expression used when the operator
// hasn't configured one. "0 3 * * *" = daily at 03:00 UTC. Both the
// scheduler fallback and config.DefaultConfig() pin to this value
// so the install is never accidentally unscheduled.
const DefaultSchedule = "0 3 * * *"

// MustParse is the convenience wrapper for callers (scheduler
// fallback, tests) that want the default schedule without handling
// the error path. It panics on parse failure because DefaultSchedule
// is hard-coded — if it ever errors, the test fixture has rotted.
func MustParse(expr string) Schedule {
	s, err := Parse(expr)
	if err != nil {
		panic("backup: hard-coded cron expression is invalid: " + err.Error())
	}
	return s
}

// Next returns the next UTC time strictly after t that matches the
// schedule. t is converted to UTC and truncated to the minute
// before walking.
//
// The walk order is fixed: month → day → hour → minute. Each
// field that doesn't match bumps to the start of the next coarser
// field and the loop re-checks from the top. The algorithm bounds
// the walk to ~5 years out so a structurally unsatisfiable
// schedule (e.g. "0 0 30 2 *" — Feb 31 never exists) returns
// zero instead of looping forever; Parse rejects most obviously
// bad expressions up front. 5 years is the buffer that lets
// leap-year-only schedules (e.g. "0 0 29 2 *") find their next
// fire even when called between leap years.
//
// Returned time is in UTC.
func (s Schedule) Next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	// Five years is the buffer that lets "Feb 29"-style schedules
	// land on the next leap year (max gap 4 years). Worst-case
	// day-by-day walks over the window stay under ~2000
	// iterations — cheap enough for a once-per-fire scheduler.
	bound := t.AddDate(5, 0, 0)
	for t.Before(bound) {
		// Month — bump to the first instant of the next month if
		// the current month isn't allowed. February in a non-leap
		// year is handled implicitly: the day walk below skips
		// past the 28th and the next-month bump lands on March 1.
		if !s.month.contains(int(t.Month())) {
			t = nextMonthStart(t)
			continue
		}
		// Day — combined dom/dow check with cron's OR semantic.
		if !s.dayMatches(t) {
			t = nextDayStart(t)
			continue
		}
		// Hour — bump to the top of the next hour if the current
		// hour isn't allowed.
		if !s.hour.contains(t.Hour()) {
			t = nextHourStart(t)
			continue
		}
		// Minute — single-step walk within the hour. Withdrawn to
		// add 1 minute; the loop re-checks the field above. The
		// step count is bounded by 60, fine for any sane schedule.
		if !s.minute.contains(t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// dayMatches implements cron's dom/dow OR rule: when both fields
// are restricted, *either* hit makes the date match; otherwise
// only the restricted field matters. Two unrestricted fields match
// every day.
func (s Schedule) dayMatches(t time.Time) bool {
	domBit := s.dom.contains(t.Day())
	dowBit := s.dow.contains(int(t.Weekday()))
	switch {
	case s.domRestricted && s.dowRestricted:
		return domBit || dowBit
	case s.domRestricted:
		return domBit
	case s.dowRestricted:
		return dowBit
	default:
		return true
	}
}

// nextMonthStart returns the first instant of the month after t's
// month. December wraps to January of the next year. The returned
// time is at 00:00 UTC so the outer loop re-checks from minute=0.
func nextMonthStart(t time.Time) time.Time {
	y, m, _ := t.Date()
	if m == time.December {
		return time.Date(y+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
}

// nextDayStart returns 00:00 UTC of the day after t.
func nextDayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
}

// nextHourStart returns the top of the next hour after t, in UTC.
// A 23:00 input wraps to 00:00 of the next day via time.Date's
// normalization.
func nextHourStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
}
