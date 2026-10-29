-- 0020 — Catalog: what a branch's last push could not accept.
--
-- `glossa push` sends a branch's whole catalog; items whose source is
-- not a valid MessageFormat 2 message are rejected one by one, and the
-- push still applies the rest. Until now the rejection lived only in
-- the push's own HTTP response, so nothing downstream could say what
-- was wrong — and the Glossa PR check has to report exactly that
-- ("invalid messages (structural QA)", RFC 0004 §6.4), minutes later,
-- from stored state.
--
-- So the branch remembers its last push's rejected items, the way it
-- already remembers the keys that push no longer had. Both are reports,
-- not decisions: a branch never obsoletes anything, and an invalid item
-- was never stored in the first place. The next push replaces the list.

ALTER TABLE catalog_branches
    ADD COLUMN invalid_items jsonb NOT NULL DEFAULT '[]'
        CHECK (jsonb_typeof(invalid_items) = 'array');
