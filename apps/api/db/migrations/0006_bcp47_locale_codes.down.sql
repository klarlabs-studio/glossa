-- Reverse 0006. Fails loudly rather than truncating if any code is
-- longer than 8 characters; rename or delete those locales first.
ALTER TABLE projects ALTER COLUMN default_locale TYPE VARCHAR(8);
ALTER TABLE locales  ALTER COLUMN code           TYPE VARCHAR(8);
