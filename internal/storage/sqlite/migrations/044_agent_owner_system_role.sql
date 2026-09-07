-- ============================================================================
-- 044_agent_owner_system_role.sql — synthetic agent-owner gets role 'system'
-- (task 171, security hardening)
-- ============================================================================
-- The agent service (internal/service/agent) lazily creates a synthetic
-- user agent-owner@orenda.local as the owner of api_tokens rows. Its
-- password is the constant "unusable" — visible in source. Before this
-- hardening the row was created WITHOUT an explicit role, and
-- domain/user.Validate defaults an empty role to 'owner', so on every
-- existing instance the synthetic user sat with role='owner' and anyone
-- who knew the constant email+password could mint a full owner session
-- via POST /api/v1/auth/login.
--
-- Three-part fix (T171):
--   * agent.go ensureOwner now creates the row with role='system'
--     (fresh databases);
--   * THIS migration heals instances whose synthetic user already
--     exists with a non-system role — scoped strictly to the constant
--     email, idempotent (0 rows on re-run), leaves every real user
--     untouched;
--   * loginHandler rejects logins for non-owner roles with the same
--     401 shape as invalid credentials (defense in depth, also
--     future-proofs other non-owner roles).
--
-- scopesForRole already maps 'system' to no scopes, so even a
-- system-role JWT would carry none.
--
-- Down: restores the historical (pre-hardening) state role='owner' for
-- the synthetic user — the state Validate's default produced.

UPDATE users
SET role = 'system'
WHERE lower(email) = 'agent-owner@orenda.local'
  AND role <> 'system';
