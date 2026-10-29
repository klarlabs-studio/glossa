-- 0025 — a check row remembers that its pull request came from a fork.
--
-- RFC 0004 §6.3: a pull request from a fork gets no OIDC token and no
-- secrets, so its CI can never upload this commit's messages or usages.
-- The thirty-minute wait exists for a commit whose CI has not run *yet*;
-- for a fork there is nothing to wait for, so the worker needs to know
-- where the head lives and conclude `neutral` on the first attempt.
--
-- `false` is right for every existing row: until now a fork's pull
-- request was returned early and given no check row at all.
ALTER TABLE integration_github_checks
    ADD COLUMN from_fork boolean NOT NULL DEFAULT false;
