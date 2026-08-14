import { describe, it, expect } from 'vitest';

import {
  dueStateClasses,
  formatDueDate,
  isBlocked,
  priorityBorderClass,
  progressLabel,
  taskDueState,
} from './taskCardBadges';

describe('taskDueState', () => {
  it('returns "done" when status is done', () => {
    expect(
      taskDueState({ status: 'done', due_at: '2020-01-01T00:00:00Z' }, new Date('2026-08-11')),
    ).toBe('done');
  });

  it('returns "done" when completed_at is set', () => {
    expect(
      taskDueState(
        { status: 'todo', completed_at: '2026-08-10T00:00:00Z' },
        new Date('2026-08-11'),
      ),
    ).toBe('done');
  });

  it('returns "none" when due_at is missing', () => {
    expect(taskDueState({ status: 'todo' }, new Date())).toBe('none');
    expect(taskDueState({ status: 'todo', due_at: null }, new Date())).toBe('none');
  });

  it('returns "overdue" when due_at is before today', () => {
    expect(
      taskDueState(
        { status: 'todo', due_at: '2020-01-01T00:00:00Z' },
        new Date('2026-08-11T00:00:00Z'),
      ),
    ).toBe('overdue');
  });

  it('returns "today" when due_at is the same calendar day', () => {
    // Use mid-day UTC for both ends — tested environment may run
    // in any timezone; noon UTC is unambiguous.
    expect(
      taskDueState(
        { status: 'todo', due_at: '2026-08-11T12:00:00Z' },
        new Date('2026-08-11T12:00:00Z'),
      ),
    ).toBe('today');
  });

  it('returns "upcoming" when due_at is later than today', () => {
    expect(
      taskDueState(
        { status: 'todo', due_at: '2026-09-01T00:00:00Z' },
        new Date('2026-08-11T00:00:00Z'),
      ),
    ).toBe('upcoming');
  });

  it('returns "none" for an unparseable due_at', () => {
    expect(taskDueState({ status: 'todo', due_at: 'not-a-date' }, new Date())).toBe('none');
  });
});

describe('dueStateClasses', () => {
  it('returns bg-red-100 for overdue', () => {
    expect(dueStateClasses('overdue')).toContain('bg-red-100');
  });
  it('returns bg-amber-100 for today', () => {
    expect(dueStateClasses('today')).toContain('bg-amber-100');
  });
  it('returns bg-emerald-100 for done', () => {
    expect(dueStateClasses('done')).toContain('bg-emerald-100');
  });
  it('returns empty for none', () => {
    expect(dueStateClasses('none')).toBe('');
  });
});

describe('formatDueDate', () => {
  it('formats same year as "day month"', () => {
    expect(formatDueDate('2026-08-12T00:00:00Z', new Date('2026-08-11T00:00:00Z'))).toMatch(
      /12\s+авг/,
    );
  });
  it('formats other year with year appended', () => {
    expect(formatDueDate('2027-01-15T00:00:00Z', new Date('2026-08-11T00:00:00Z'))).toMatch(/2027/);
  });
  it('returns empty for null / unparseable', () => {
    expect(formatDueDate(null)).toBe('');
    expect(formatDueDate('not-a-date')).toBe('');
  });
});

describe('priorityBorderClass', () => {
  it('red for urgent', () => {
    expect(priorityBorderClass('urgent')).toContain('border-l-red-500');
  });
  it('orange for high', () => {
    expect(priorityBorderClass('high')).toContain('border-l-orange-400');
  });
  it('slate for low', () => {
    expect(priorityBorderClass('low')).toContain('border-l-slate-300');
  });
  it('empty for medium (default)', () => {
    expect(priorityBorderClass('medium')).toBe('');
    expect(priorityBorderClass(undefined)).toBe('');
  });
});

describe('progressLabel', () => {
  it('returns done/total when total > 0', () => {
    expect(progressLabel(2, 5)).toBe('2/5');
  });
  it('returns empty when total is 0', () => {
    expect(progressLabel(0, 0)).toBe('');
    expect(progressLabel(5, 0)).toBe('');
  });
});

describe('isBlocked', () => {
  it('true when count > 0', () => {
    expect(isBlocked(1)).toBe(true);
    expect(isBlocked(3)).toBe(true);
  });
  it('false when count is 0 or undefined', () => {
    expect(isBlocked(0)).toBe(false);
    expect(isBlocked(undefined)).toBe(false);
  });
});
