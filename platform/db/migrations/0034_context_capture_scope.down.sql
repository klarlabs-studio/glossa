-- The index is a lookup path and holds no data: dropping it costs the
-- two-sighting rule its speed and nothing else.
DROP INDEX IF EXISTS context_captures_scope;
