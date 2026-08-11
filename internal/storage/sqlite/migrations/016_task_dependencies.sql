-- ============================================================================
-- 016_task_dependencies.sql — task blocking graph
-- ============================================================================
-- Phase 15: a task can declare that it depends on one or more other
-- tasks; it cannot be claimed by an agent until every blocker is in
-- `done` state. The graph is many-to-many with ON DELETE CASCADE so
-- removing a task cleans up its blockers and dependents.
--
-- Design notes:
--   - (task_id, depends_on_task_id) is the primary key. No duplicates;
--     a task can depend on the same blocker at most once.
--   - The forward index `idx_task_deps_task` covers the hot path:
--     "what blocks this task?". The reverse index `idx_task_deps_depends_on`
--     supports the inverse lookup "what does finishing this task unblock?".
--     Both are needed for the agent UI ('this is what you can take next').
--   - We don't store cycle prevention here — the service does a DFS
--     walk on AddDependency so we can return a 422 with the cycle path
--     for diagnostics instead of a generic FK violation.

CREATE TABLE task_dependencies (
    task_id              TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_task_id   TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, depends_on_task_id)
);

CREATE INDEX idx_task_deps_task        ON task_dependencies(task_id);
CREATE INDEX idx_task_deps_depends_on  ON task_dependencies(depends_on_task_id);
