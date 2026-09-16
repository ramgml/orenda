-- T16: dialog tutor. One row per student question or agent reply in
-- a lesson-scoped tutoring thread.
--
-- Thread identity is (lesson_id, user_id): the context the tutor
-- agent answers within is the lesson's content_md + quizzes. There
-- is deliberately no "awaiting" column — a thread is pending iff its
-- last message has role='user' (derived state, kept out of the
-- schema so it can never drift from the rows).
CREATE TABLE tutor_messages (
    id          TEXT PRIMARY KEY,
    lesson_id   TEXT NOT NULL REFERENCES course_lessons(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('user','agent')),
    body_md     TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_tutor_messages_lesson_user ON tutor_messages(lesson_id, user_id, created_at);
CREATE INDEX idx_tutor_messages_user ON tutor_messages(user_id);
