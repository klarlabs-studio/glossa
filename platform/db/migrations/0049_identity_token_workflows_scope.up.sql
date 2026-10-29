-- 0049 — Identity: the `workflows` token scope (RFC 0006 §4.2; §13
-- wave 3).
--
-- A token may now be created with `workflows` (workflows.read and
-- workflows.manage), so `glossa workflow push` can run in CI. The scope
-- is opt-in; no stored token changes. 0002 wrote the allowed scopes
-- into an unnamed column check, which Postgres named after the column.
ALTER TABLE identity_api_tokens DROP CONSTRAINT identity_api_tokens_scopes_check;
ALTER TABLE identity_api_tokens ADD CONSTRAINT identity_api_tokens_scopes_check
    CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read', 'write', 'publish', 'admin', 'workflows']);
