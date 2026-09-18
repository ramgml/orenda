-- ============================================================================
-- 046_lesson_reviews.down.sql — drop the spaced-repetition ladder (task 18)
-- ============================================================================
-- Lossy by design (down is for round-trip tests): review history is gone,
-- lesson progress (course_lessons.status) is untouched.

DROP INDEX IF EXISTS idx_lesson_reviews_user_due;
DROP INDEX IF EXISTS idx_lesson_reviews_lesson;
DROP INDEX IF EXISTS idx_lesson_reviews_due_at;
DROP TABLE IF EXISTS lesson_reviews;
