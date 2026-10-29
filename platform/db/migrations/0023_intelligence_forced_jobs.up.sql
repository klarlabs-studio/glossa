-- 0023 — Intelligence: a job that translates text already current.
--
-- Every other job exists because something is missing or stale, so both
-- the queuer and the worker refuse a message whose translation is
-- already made against the current source. The in-product editor breaks
-- that assumption (RFC 0004 §5.3): someone reading the running product
-- asks for a second opinion on text that is, by definition, current.
--
-- The queuer's check takes a flag on the request, but the worker checks
-- again when it runs — the source may have moved, or another job may
-- have filled the locale meanwhile — so the intent has to travel with
-- the job rather than with the request that made it.

ALTER TABLE intelligence_jobs
    ADD COLUMN forced boolean NOT NULL DEFAULT false;

-- The idempotency key is (message, locale, source revision, knowledge),
-- and a forced job matches an existing one by design: it is the same
-- work, asked for again. It is enqueued with requeue, which revives a
-- finished job in place, so no index changes.
