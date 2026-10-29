-- 0049 down — the narrower check comes back. A token holding
-- `workflows` is revoked and loses the scope first: a down migration
-- does not keep a working credential whose scope it no longer knows.
UPDATE identity_api_tokens
SET revoked_at = coalesce(revoked_at, now()),
    scopes = CASE WHEN cardinality(array_remove(scopes, 'workflows')) = 0 THEN ARRAY['read']
                  ELSE array_remove(scopes, 'workflows') END
WHERE 'workflows' = ANY (scopes);
ALTER TABLE identity_api_tokens DROP CONSTRAINT identity_api_tokens_scopes_check;
ALTER TABLE identity_api_tokens ADD CONSTRAINT identity_api_tokens_scopes_check
    CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read', 'write', 'publish', 'admin']);
