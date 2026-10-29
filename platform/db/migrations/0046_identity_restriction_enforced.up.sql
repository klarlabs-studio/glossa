-- 0046 — Identity: the principal sees its restriction (RFC 0006 §3.3,
-- §4.1; §13 wave 2).
--
-- 0041 stored project scope, vendor and visibility and deliberately
-- gave glossa_system no grant on them, so the pre-tenant lookups that
-- build a request's principal could not read them while nothing
-- enforced them. Wave 2 enforces them in authz, so those lookups now
-- read them: a member's project scope and visibility, and a token's
-- project scope. vendor_id stays out of reach — authorization never
-- needs to know which vendor a member works for, only that they see
-- their assignments.
GRANT SELECT (projects, visibility) ON identity_members TO glossa_system;
GRANT SELECT (projects) ON identity_api_tokens TO glossa_system;
