package cli

import (
	"context"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa check` puts its run on the record (RFC 0005 §9,
// `createCheckRun`).
//
// M4's exit criterion is `glossa check` gating CI in every dogfood
// product. Until the run is recorded, that gate is invisible
// everywhere else: Studio's quality view, `listFindings`, the project
// summary and the findings-by-day rollup stay empty for exactly the
// projects the milestone targets, because nothing outside the platform
// binary ever wrote a finding. Recording closes that, and it closes it
// without a product changing anything — the same command, the same CI
// token, the same permissions.
//
// **Default in CI, off on a laptop, `--record` either way.** The
// default has to serve the exit criterion without a new line in a
// workflow file, so in CI it records. It is deliberately *not* the
// default everywhere else:
//
//   - A developer running `glossa check` in a loop while editing a
//     message would post a run every few seconds, and each one becomes
//     the project's newest — the run every dashboard, `listFindings`
//     and the summary read. The team would be looking at one laptop's
//     half-finished branch instead of at what CI last decided.
//   - A local run may carry local overrides: `glossa.yaml`'s `check:`
//     block, `--fail-on`, `--require-complete`, `--layer`. The
//     pull-request check ignores all of them on purpose (RFC 0005 §4.2,
//     §14 decision 3), so what a local run computed is not what the
//     project's policy computes.
//   - Recording is a write. Silently writing to the shared record on a
//     command a developer reaches for to answer a question about their
//     own working copy is not a default anyone asked for.
//
// What the server does with what is sent is the other half of why this
// is safe: it computes every fingerprint itself over the catalog
// message the key resolved to, re-grades every severity against the
// project's stored policy and reaches its own conclusion. An overridden
// local run cannot therefore put an overridden verdict on the record —
// only the findings, which the project's policy then grades.
//
// `--offline` never records, and neither does a run that fell back to
// the cached policy because the server was out of reach: there is no
// server to record to, and a run graded against a cache is not the
// project's verdict.

// recordJSON is what became of putting this run on the record.
type recordJSON struct {
	// Recorded says the server stored the run.
	Recorded bool `json:"recorded"`
	// Run is the stored run's id.
	Run string `json:"run,omitempty"`
	// Conclusion is what the project's policy concluded on the server.
	// It can differ from this run's when the policy moved since the
	// check fetched it, or when local overrides graded this run.
	Conclusion string `json:"conclusion,omitempty"`
	// Why says why nothing was recorded. It is set exactly when
	// Recorded is false, and it never changes the exit code: a check
	// reports what it found, and failing to file the report is not the
	// same as failing the check.
	Why string `json:"why,omitempty"`
}

// wantsRecord decides whether this run is recorded: --record and
// --record=false decide outright, and otherwise CI does.
func (f checkFlags) wantsRecord(getenv func(string) string) bool {
	if f.record != nil {
		return *f.record
	}
	return inCI(getenv)
}

// inCI reports whether this is a CI runner. `CI` is the convention
// every provider follows; the three named after it are the ones
// detectBuild already reads, so a runner that sets only its own
// variable is still recognized here.
func inCI(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("CI"))) {
	case "", "0", "false", "no":
	default:
		return true
	}
	return getenv("GITHUB_ACTIONS") == "true" || getenv("GITLAB_CI") != "" || getenv("BUILDKITE") == "true"
}

// recordCheck files the run with the server and says what became of it.
// It returns nil when the run was not meant to be recorded at all, so
// `--json` carries a `record` member exactly when recording was asked
// for — by a flag or by CI.
// build is what this run is of: the branch it graded and the commit it
// graded there. `glossa check` detects it; `glossa capture --check`
// already resolved it for the capture document it is about to upload,
// and hands that in, so the run and the captures name one commit.
func (inv *invocation) recordCheck(
	ctx context.Context, run *checkSubject, out checkJSON, f checkFlags, build buildRef,
) *recordJSON {
	if !f.wantsRecord(inv.env.getenv) {
		return nil
	}
	if why := run.cannotRecord(f); why != "" {
		return &recordJSON{Why: why}
	}
	if !validBranch(build.Branch) {
		return &recordJSON{Why: "a run is of a branch, and this one has none: set GLOSSA_BRANCH"}
	}
	commit := build.Commit
	if !commitPattern.MatchString(commit) {
		// A run without a commit is still a run of a branch. Sending a
		// half-commit would be refused; sending none is the true answer.
		commit = ""
	}
	stored, err := run.client.RecordCheck(ctx, run.scope, remote.RecordCheckRun{
		Ref: build.Branch, Commit: commit, Trigger: string(domain.TriggerCLI),
		Layers: out.Layers, Findings: out.Findings, StartedAt: run.startedAt,
	})
	if err != nil {
		return &recordJSON{Why: asError(err).Error()}
	}
	rec := &recordJSON{Recorded: true, Run: stored.Id}
	if stored.Conclusion != nil {
		rec.Conclusion = string(*stored.Conclusion)
	}
	return rec
}

// cannotRecord says why this run cannot be filed, or "" when it can.
func (s *checkSubject) cannotRecord(f checkFlags) string {
	switch {
	case f.offline:
		return "--offline has no server to record to"
	case s.degraded:
		return "the server was out of reach, so this run graded against the cached policy and is not the project's verdict"
	case s.client == nil:
		return "this run has no server"
	}
	return ""
}

// printRecord says what became of the record, after the findings and
// before the exit code — which recording never changes.
func printRecord(p *printer, rec *recordJSON) {
	switch {
	case rec == nil:
		return
	case rec.Recorded:
		p.line("  %s", p.dim("recorded as check run "+rec.Run))
	default:
		p.line("  %s", p.dim("not recorded: "+rec.Why))
	}
}
