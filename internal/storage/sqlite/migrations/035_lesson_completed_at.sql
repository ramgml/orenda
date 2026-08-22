-- Migration 035 — lesson completion timestamps for pace metrics.
--
-- Before: course_lessons had only a status enum (locked|open|done)
-- without a completion timestamp. Phase 32.12 (LMS pace adaptation)
-- needs to compute rolling velocity (lessons done per week over
-- 14 days) and "last_completed_at" for the agent-side course list.
-- status flips to 'done' via Service.CompleteLesson, but without a
-- timestamp we can't tell when the flip happened — the only signal
-- was courses.updated_at which is too coarse.
--
-- After:
-- - completed_at TEXT NULL on course_lessons. Set by the service
--   layer when status flips locked|open → done. NULL = not done
--   (or done before this migration shipped; legacy rows are
--   treated as "no completion timestamp" → velocity is 0 even if
--   status=done, which is conservative — planner sees slower pace
--   than reality rather than faster).
-- - index on (module_id, completed_at) for the velocity query
--   (filtered per-course via the join).
--
-- Backfill intentionally skipped: course_lessons has no updated_at
-- column, and inferring a completion timestamp from any other
-- source would lie about historical timing. Legacy rows have NULL
-- completed_at even when status=done; the velocity classifier
-- treats those as "before the window" and contributes 0.

ALTER TABLE course_lessons ADD COLUMN completed_at TEXT;
CREATE INDEX idx_course_lessons_completed ON course_lessons(module_id, completed_at) WHERE completed_at IS NOT NULL;