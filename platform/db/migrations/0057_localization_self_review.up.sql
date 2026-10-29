-- 0057 — a review revision records that the reviewer wrote the text (#73).
--
-- RFC 0006 §15 Q6 (amended 2026-10-08): an author never approves or
-- rejects their own translation when someone else could review it. When
-- nobody else could (a solo individual tenant, or no other member holds
-- translations.review for the locale and project), the author may, and
-- the review revision says so. The flag is a fact about the decision, not
-- text: history is append-only, so older rows keep false.
ALTER TABLE localization_translation_revisions
    ADD COLUMN self_review boolean NOT NULL DEFAULT false;
