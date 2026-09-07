-- ============================================================================
-- 044_agent_owner_system_role.down.sql — restore the pre-hardening role
-- ============================================================================
-- Reverts the synthetic agent-owner user to role='owner' — the state the
-- domain/user.Validate default produced before the T171 hardening. The
-- login gate in loginHandler stays in place regardless, so reverting the
-- data does not reopen the login hole by itself.

UPDATE users
SET role = 'owner'
WHERE lower(email) = 'agent-owner@orenda.local'
  AND role = 'system';
