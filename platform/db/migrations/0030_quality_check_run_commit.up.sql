-- 0030 — a check run remembers the commit it graded.
--
-- RFC 0005 §2.2 rule 3 names a run by its ref — a branch or an
-- environment — because that is what a dashboard groups by. The pull
-- request, though, grades one *commit*: §12.3's exit criterion is that
-- the CLI and the pull-request check of the same commit reach the same
-- conclusion and the same per-layer counts, and `glossa findings
-- --commit` asks the same question from the terminal. A branch moves;
-- the commit a verdict was about does not, so the ref alone cannot
-- answer it.
--
-- '' is right for every existing row and for every run that is not
-- about a commit (an environment's run, a write-time job): the filter
-- then simply never selects it.
ALTER TABLE quality_check_runs
    ADD COLUMN commit_sha text NOT NULL DEFAULT '' CHECK (commit_sha = '' OR commit_sha ~ '^[0-9a-f]{40}$');

-- The runs of one commit, newest first: what `glossa findings --commit`
-- and the pull-request check read.
CREATE INDEX quality_check_runs_commit ON quality_check_runs (project_id, commit_sha, started_at DESC, id DESC)
    WHERE commit_sha <> '';
