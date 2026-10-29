package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	quality "go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The ports of the Glossa PR check (RFC 0004 §6.4): the queue the
// worker claims from, and the read model the report is rendered out of.

// CheckQueue is the check queue across tenants (system scope
// integration.github), the way DeliveryInbox is the webhook inbox. One
// row is one pull request, so claiming it is what keeps a single job
// per pull request — and therefore a single writer of the one sticky
// comment.
type CheckQueue interface {
	// Open records a pull request's check and makes it due now. An
	// existing row keeps its sticky comment and moves to the new head
	// SHA, which starts the wait for CI again.
	Open(ctx context.Context, c domain.Check) (domain.Check, error)
	// Rerun is `check_run.rerequested`: the check runs and the
	// annotations already sent to them are forgotten, because GitHub
	// makes new runs, and the row becomes due.
	Rerun(ctx context.Context, repository int64, headSHA string, now time.Time) (int, error)
	// Wake makes the branch's checks in these repositories due again,
	// because something the report mentions has changed.
	Wake(ctx context.Context, repositories []int64, branch string, now time.Time) (int, error)
	// Claim leases the oldest check due at now; ok is false when none is.
	// now is the service's clock, the one every available_at was written
	// with, never the database's: a check made due by the app's clock and
	// claimed by Postgres's could wait out the difference between them.
	Claim(ctx context.Context, now time.Time, lease time.Duration) (c domain.Check, ok bool, err error)
	// Save writes a claimed check back and releases the lease.
	Save(ctx context.Context, c domain.Check, available time.Time) error
	// Retry hands a claimed check back after delay, keeping what it
	// learned.
	Retry(ctx context.Context, c domain.Check, now time.Time, delay time.Duration, failure string) error
	// Expire makes every queued check whose head SHA was requested at or
	// before deadline due again; the worker completes them `neutral`.
	Expire(ctx context.Context, deadline, now time.Time) (int, error)
	// Depth reports the checks waiting to run (the §11 metric).
	Depth(ctx context.Context) (int, error)
	// DropRepository forgets a repository's checks.
	DropRepository(ctx context.Context, repository int64) (int, error)
	// Check reads one pull request's check.
	Check(ctx context.Context, repository int64, pullRequest int) (domain.Check, error)
}

// The check reports quality.Finding, the one shape a finding has
// (RFC 0005 §2.1): a finding whose locus has a file and a line becomes
// a GitHub annotation, and the rest are summary only. It used to have
// its own CheckFinding, which disagreed with the CLI's and dropped the
// spans, the subject and the qualifier on the way in.

// InvalidMessage is one item the branch's last push could not accept.
type InvalidMessage struct{ Key, Code, Detail string }

// KeyConflict is a new key two open branches propose with different
// source (RFC 0004 §4.1).
type KeyConflict struct {
	Key      string
	Branches []string
}

// BranchStatus is Catalog's branch status as the check reports it —
// Integration's own shape, never Catalog's Go types.
type BranchStatus struct {
	Name       string
	PR         *int
	HeadCommit string
	State      string
	// PreviewURL is where CI deployed the branch's preview, if anywhere
	// (`glossa preview register --url`).
	PreviewURL      string
	NewKeys         []string
	SourceProposals []string
	Removed         []string
	Invalid         []InvalidMessage
	Conflicts       []KeyConflict
	// Outdated counts, per locale, the translations merging the branch
	// will make outdated.
	Outdated map[string]int
}

// BranchQuality is what Localization and Knowledge say about a branch:
// how far its new keys have got, per locale, and the QA findings on its
// messages.
type BranchQuality struct {
	// Locales are the project's target locales, in order.
	Locales []string
	// Untranslated counts, per locale, the branch's new keys with no
	// usable translation yet.
	Untranslated map[string]int
	// Findings are the QA warnings the server holds on the branch's
	// messages: max_length (Localization stores it with the text) and
	// terminology (M2).
	Findings []quality.Finding
}

// UnknownKey is a usage of a key the catalog did not know at ingest
// (RFC 0004 §2.2), with where the product asks for it.
type UnknownKey struct {
	Key  string
	File string
	Line int
}

// UsageSite is where the product asks for a key: one usage Context
// ingested for this branch's build.
//
// It is what turns a finding into an annotation. A finding from
// `glossa check` is about a message in a catalog and carries no file of
// its own, because a catalog is not a file; Context is the context that
// knows where a key is used, and filling the locus in at report time is
// what its package doc has said since M4 (quality/domain, package doc).
type UsageSite struct {
	File string
	Line int
}

// BranchUsages is what Context says about the branch's current builds.
type BranchUsages struct {
	// Builds counts the current builds the view resolved to; 0 means CI
	// has uploaded nothing for this branch.
	Builds int
	// Commits are those builds' commits. The check reads its own
	// readiness from them rather than from an event, so a build that is
	// ingested twice, late or never changes nothing but the answer.
	Commits []string
	Unknown []UnknownKey
	// Where the product asks for each key the branch's build uses, one
	// site per key. It locates the findings of the run the check
	// renders, and nothing else: it changes no count and no verdict,
	// only whether a reviewer sees a finding where the problem is.
	Where map[string]UsageSite
	// Captured and NotCaptured count the branch's active messages with
	// and without a capture (RFC 0004 §3).
	Captured, NotCaptured int
}

// RecordedRun is the check run CI recorded for a commit — the run the
// pull request renders (RFC 0005 §12.3).
//
// This is the whole of the exit criterion. `glossa check` computes
// every layer over the project and records what it found
// (`createCheckRun`); the pull request used to compute a second,
// narrower thing and present it as the same answer. It cannot: one
// read every layer and the whole project, the other read the warnings
// stored with each translation on the branch's own keys. Two
// computations tested for agreement will disagree. One computation,
// rendered twice, cannot.
type RecordedRun struct {
	ID uuid.UUID
	// Ref is the branch the run was of, and Commit the commit it
	// graded. The check matches on the commit: a branch moves, and a
	// verdict belongs to the commit it was about.
	Ref, Commit string
	// Trigger says what asked for the run ("cli" for `glossa check`).
	Trigger string
	// PolicyVersion is the version the run graded itself against when it
	// was recorded. The report grades again against the version that
	// applies to *this* pull request, which is not the same thing while
	// a grace is running (RFC 0005 §4.3).
	PolicyVersion int
	// Layers are the layers the run computed, so the report can say
	// what was looked at rather than only what was found.
	Layers []quality.Layer
	// Findings are the run's findings as they stand now, with the
	// project's live waivers applied — the same read `glossa findings`
	// and Studio make.
	Findings []quality.Finding
	// Truncated says the run holds more findings than were read. The
	// summary says so rather than quietly reporting a smaller run.
	Truncated   bool
	StartedAt   time.Time
	CompletedAt time.Time
}

// CheckSources is the read model the check renders from: the other
// bounded contexts' application services, reached through ports as
// RFC 0002 §4 requires, so each keeps checking the caller's
// permissions.
type CheckSources interface {
	// RecordedRun is the newest check run recorded for commit, and
	// false where nothing has recorded one. It is Quality's, read
	// through this port and never out of Quality's tables.
	RecordedRun(ctx context.Context, project uuid.UUID, commit string) (RecordedRun, bool, error)
	// RecordsRuns reports whether the project has ever recorded a
	// reported check run — `glossa check` in CI, the pull-request check
	// or an explicit API call, of any commit — among the runs Quality
	// still keeps. It is how the check knows this repository's CI runs
	// `glossa check`, and so whether a commit without a run yet is worth
	// waiting for. It is asked on the readiness path: a yes-or-no, one
	// bounded read, never a listing.
	RecordsRuns(ctx context.Context, project uuid.UUID) (bool, error)
	// Policy is the project's check policy — the same `require_complete`
	// and `fail_on` as `glossa check` (RFC 0004 §6.4).
	Policy(ctx context.Context, project uuid.UUID) (checkpolicy.Policy, error)
	// BranchStatus is Catalog's branch report.
	BranchStatus(ctx context.Context, project uuid.UUID, branch string) (BranchStatus, error)
	// BranchQuality reports the branch's new keys per locale and the QA
	// findings on its messages. keys are the branch's own (new keys and
	// source proposals).
	BranchQuality(ctx context.Context, project uuid.UUID, branch string, keys []string) (BranchQuality, error)
	// BranchUsages reports the branch's unknown keys and capture counts.
	BranchUsages(ctx context.Context, project uuid.UUID, branch string) (BranchUsages, error)
	// ManifestURL is where the branch environment's manifest is served,
	// or "" when there is no environment or the deployment does not
	// announce its edge.
	ManifestURL(ctx context.Context, project uuid.UUID, branch string, pr int) (string, error)
}

// CheckMetrics records the check series of RFC 0004 §11. Every method
// must be safe for concurrent use and must not block.
type CheckMetrics interface {
	// CheckCompleted counts one completed check by conclusion, with the
	// latency from the pull-request event to the completed check.
	CheckCompleted(conclusion string, latency time.Duration)
	// CheckHandled counts one worker attempt by outcome ("done",
	// "waiting", "ignored", "retry", "failed") and how long it took.
	CheckHandled(outcome string, d time.Duration)
	// CommentUpserted counts one sticky-comment write by outcome ("ok",
	// "failed").
	CommentUpserted(outcome string)
	// QueueDepth reports the checks waiting to run.
	QueueDepth(n int)
}

// NoCheckMetrics records nothing.
type NoCheckMetrics struct{}

// CheckCompleted implements CheckMetrics.
func (NoCheckMetrics) CheckCompleted(string, time.Duration) {}

// CheckHandled implements CheckMetrics.
func (NoCheckMetrics) CheckHandled(string, time.Duration) {}

// CommentUpserted implements CheckMetrics.
func (NoCheckMetrics) CommentUpserted(string) {}

// QueueDepth implements CheckMetrics.
func (NoCheckMetrics) QueueDepth(int) {}
