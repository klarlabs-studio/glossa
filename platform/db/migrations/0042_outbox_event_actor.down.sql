ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS outbox_events_actor_spelled;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS actor;
