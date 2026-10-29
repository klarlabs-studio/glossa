DROP TABLE IF EXISTS catalog_proposals;
DROP TABLE IF EXISTS catalog_branches;

-- Proposed messages were never released; without branches they are
-- obsolete.
UPDATE localization_messages SET state = 'obsolete' WHERE state = 'proposed';
ALTER TABLE localization_messages DROP CONSTRAINT localization_messages_state_check;
ALTER TABLE localization_messages ADD CONSTRAINT localization_messages_state_check
    CHECK (state IN ('active', 'obsolete'));

UPDATE catalog_messages SET state = 'obsolete' WHERE state = 'proposed';
ALTER TABLE catalog_messages DROP CONSTRAINT catalog_messages_state_check;
ALTER TABLE catalog_messages ADD CONSTRAINT catalog_messages_state_check
    CHECK (state IN ('active', 'obsolete'));
