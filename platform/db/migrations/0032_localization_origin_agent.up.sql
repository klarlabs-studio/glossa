-- 0032 — `agent` is an origin of its own (RFC 0005 §7.3).
--
-- RFC 0005 §7.3 says an MCP write carries provenance `agent`. The six
-- origins 0004 wrote down had no such value, so M4 wave 3's write tools
-- recorded `ai` with origin_detail {"via":"mcp","tool":"…"} instead. An
-- autonomous agent writing through a long-lived token is not a person
-- clicking "translate with AI", and a provenance you can only read by
-- unpacking a JSON object is not one you can filter, group or count on.
-- Translations carry provenance (AGENTS.md), so the distinction belongs
-- in the column.
--
-- This widens the two CHECK constraints and touches nothing else. **No
-- row is rewritten.** Translations and revisions written through MCP
-- before this keep `ai`, because `ai` is what was true when they were
-- written: the revision log is append-only, and history is not edited
-- to match a vocabulary that arrived later. Those rows are still
-- recognizable — their origin_detail says {"via":"mcp"}, and it stays.

ALTER TABLE localization_translations
    DROP CONSTRAINT localization_translations_origin_check;
ALTER TABLE localization_translations
    ADD CONSTRAINT localization_translations_origin_check
    CHECK (origin IN ('human', 'ai', 'agent', 'translation_memory',
                      'machine_translation', 'import', 'adaptation'));

ALTER TABLE localization_translation_revisions
    DROP CONSTRAINT localization_translation_revisions_origin_check;
ALTER TABLE localization_translation_revisions
    ADD CONSTRAINT localization_translation_revisions_origin_check
    CHECK (origin IN ('human', 'ai', 'agent', 'translation_memory',
                      'machine_translation', 'import', 'adaptation'));
