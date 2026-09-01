import { describe, expect, it } from 'vitest';

import { activityDetails } from './activityDetails';

// Task 117: the task.moved payload carries `column_name` (the target
// column's name snapshotted at event time). Rows written before 117
// (and rows whose column lookup failed at write time) have no
// `column_name` and must fall back to the legacy `→ <column_id>` UUID
// form instead of breaking.
describe('activityDetails — task.moved column names (task 117)', () => {
  it('renders the column name when the payload carries column_name', () => {
    expect(
      activityDetails(
        'task.moved',
        '{"column_id":"01a0col000000000000000000","column_name":"In Review","position":1024}',
      ),
    ).toBe('→ In Review');
  });

  it('falls back to the column_id UUID for legacy rows without column_name', () => {
    expect(
      activityDetails('task.moved', '{"column_id":"01a0col000000000000000000","position":1024}'),
    ).toBe('→ 01a0col000000000000000000');
  });

  it('renders nothing when neither column_name nor column_id is present', () => {
    expect(activityDetails('task.moved', '{"position":1024}')).toBe('');
    expect(activityDetails('task.moved', '{}')).toBe('');
  });

  it('treats a blank column_name as missing and falls back to the UUID', () => {
    expect(
      activityDetails(
        'task.moved',
        '{"column_id":"01a0col000000000000000000","column_name":"   "}',
      ),
    ).toBe('→ 01a0col000000000000000000');
  });
});
