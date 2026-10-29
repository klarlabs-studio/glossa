-- 0006 — Localization: an index for listing a project's translations
-- across messages in key order (GET …/translations) and for the
-- per-locale status summary (GET …/translation-stats).
--
-- Both walk Localization's message projection by project. The listing
-- orders by (key, message_id) — keys can briefly repeat while renames
-- are delivered out of order, so the ID breaks ties — and pages on that
-- keyset; the 0004 index on (project_id, key text_pattern_ops) serves
-- LIKE prefixes but not ORDER BY key. The INCLUDEd columns are what
-- both queries read from the message (namespace, state, the current
-- source revision against which "outdated" is derived), so neither has
-- to visit the heap for them. Translations are then reached through
-- their UNIQUE (message_id, locale) index, and the summary groups them
-- through localization_translations_project_locale.
--
-- Plain CREATE INDEX: golang-migrate runs each file in a transaction,
-- and the table is small enough to lock briefly at this stage.

CREATE INDEX localization_messages_project_order
    ON localization_messages (project_id, key, message_id)
    INCLUDE (namespace, state, source_revision);
