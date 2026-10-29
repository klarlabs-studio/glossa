-- 0011 — Integration: every import result says where its item is in
-- the file. line and col (0009) located only the problem that failed a
-- malformed file; the readers now record each entry's position, and
-- ref names the item in the format's own terms (an XLIFF fragment
-- identifier, a JSON pointer, a PO msgctxt and msgid, tu[n] /
-- conceptEntry[n]). Existing rows keep NULL: nothing was recorded.

ALTER TABLE integration_job_items
    ADD COLUMN ref text CHECK (char_length(ref) <= 500);
