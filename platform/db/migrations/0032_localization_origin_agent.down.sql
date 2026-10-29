-- Back to 0004's six origins.
--
-- It refuses to apply while any row says `agent`, and that is the right
-- way round: quietly rewriting an agent's translation into an AI's
-- would destroy the one distinction this migration exists to keep. A
-- deployment that really wants to go back decides what those rows
-- should say and writes it itself, first.

ALTER TABLE localization_translations
    DROP CONSTRAINT localization_translations_origin_check;
ALTER TABLE localization_translations
    ADD CONSTRAINT localization_translations_origin_check
    CHECK (origin IN ('human', 'ai', 'translation_memory',
                      'machine_translation', 'import', 'adaptation'));

ALTER TABLE localization_translation_revisions
    DROP CONSTRAINT localization_translation_revisions_origin_check;
ALTER TABLE localization_translation_revisions
    ADD CONSTRAINT localization_translation_revisions_origin_check
    CHECK (origin IN ('human', 'ai', 'translation_memory',
                      'machine_translation', 'import', 'adaptation'));
