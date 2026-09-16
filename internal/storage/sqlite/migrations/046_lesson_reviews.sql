-- ============================================================================
-- 046_lesson_reviews.sql — spaced-repetition review ladder (task 18)
-- ============================================================================
-- A lesson_review is the per-lesson spaced-repetition entry the server
-- creates when a student completes a lesson (CompleteLesson). The ladder
-- is server-side and deterministic: steps at +1 / +3 / +7 / +16 / +35
-- days (course.ReviewStepsDays). Each row walks the ladder:
--
--   step=0 due_at=completed_at+1d (written by CompleteLesson)
--   pass → step+1, due_at=now + ReviewStepsDays[new step]
--   fail → step=0, due_at=now + 1d
--   pass on the last step → completed_at set, the ladder is closed.
--
-- Ownership: user_id is the student the review belongs to. CompleteLesson
-- resolves it via ModuleCourseOwner (single-owner install today), but the
-- column is keyed per-user from day one so multi-student courses don't
-- need another migration.
--
-- due_at is RFC3339 UTC (not SQLite datetime) because the service layer
-- computes it from Go time.Time and string-compares nothing; RFC3339
-- keeps the wire shape and the stored shape identical.
--
-- Additive only: a new table + indexes, no changes to existing tables.
-- Deleting a lesson cascades (its reviews are meaningless without it).

CREATE TABLE lesson_reviews (
    id           TEXT PRIMARY KEY,
    lesson_id    TEXT NOT NULL REFERENCES course_lessons(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL,
    step         INTEGER NOT NULL DEFAULT 0,
    due_at       TEXT NOT NULL,                        -- RFC3339 UTC
    last_result  TEXT CHECK (last_result IN ('pass','fail') OR last_result IS NULL),
    completed_at TEXT,                                 -- set when the ladder is fully walked
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

-- /reviews/due scans by due date; per-user listing filters (user_id, due_at).
CREATE INDEX idx_lesson_reviews_due_at ON lesson_reviews(due_at);
CREATE INDEX idx_lesson_reviews_lesson ON lesson_reviews(lesson_id);
CREATE INDEX idx_lesson_reviews_user_due ON lesson_reviews(user_id, due_at);
