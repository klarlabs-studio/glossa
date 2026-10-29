-- BCP 47 locale identity (RFC 0001 D4).
--
-- VARCHAR(8) fit 'de' and 'de-DE' but not 'zh-Hant-TW', 'ca-ES-valencia'
-- or 'sr-Latn-RS'. 35 is the RFC 5646 §4.4.1 minimum buffer for a tag
-- without extensions or private use, which Glossa rejects anyway.
--
-- Widening a VARCHAR is metadata-only in Postgres: no rewrite, no
-- lock beyond the brief ACCESS EXCLUSIVE for the catalog update.
ALTER TABLE locales  ALTER COLUMN code           TYPE VARCHAR(35);
ALTER TABLE projects ALTER COLUMN default_locale TYPE VARCHAR(35);
