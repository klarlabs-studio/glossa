-- 0042 — Kernel: every event names its actor (RFC 0006 §6.1, §13 wave 1).
--
-- The audit trail answers "who did what", and it is projected from
-- the outbox. So the envelope carries who caused each event, spelled
-- the way identity renders an actor: `person:<uuid>`, `token:<uuid>`
-- (CI, the CLI, an MCP agent) or `system:<uuid>` (background work,
-- named stably). The publisher refuses an event without one.
--
-- Events written before this migration never recorded an actor, and
-- nothing here guesses one: they read `unknown`, which attributes them
-- to no one. (The audit backfill of wave 2 may take a payload's `by`
-- where it has one; it never invents an actor either.) Adding the
-- column with a constant default rewrites no rows. The default is then
-- dropped, so from here on an insert that names no actor fails NOT
-- NULL instead of being filed under "unknown".
--
-- `unknown` stays a value the column admits, because those rows hold
-- it; Publish refuses to write it.

ALTER TABLE outbox_events ADD COLUMN actor text NOT NULL DEFAULT 'unknown';
ALTER TABLE outbox_events ALTER COLUMN actor DROP DEFAULT;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_actor_spelled CHECK (
    actor = 'unknown'
    OR actor ~ '^(person|token|system):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
);
