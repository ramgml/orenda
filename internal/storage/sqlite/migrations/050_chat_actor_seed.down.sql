-- 050_chat_actor_seed.down.sql — remove the dashboard-chat actor seed.
-- Order matters: agents → api_tokens → users. The agents row may be
-- referenced by real study_proposals (created_by_agent FK) — only drop
-- it when no proposal points at it.
DELETE FROM agents
    WHERE id = 'chat'
      AND NOT EXISTS (
          SELECT 1 FROM study_proposals sp WHERE sp.created_by_agent = 'chat'
      );
DELETE FROM api_tokens WHERE id = 't-chat';
DELETE FROM users WHERE id = 'u-chat';
