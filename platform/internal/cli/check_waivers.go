package cli

import (
	"context"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa check` applies the project's waivers (RFC 0005 §2.3).
//
// A waiver is a finding somebody looked at, decided was fine, and wrote
// a reason for. The server applies them when it records a run
// (quality/app.RecordRun), so a waived finding is `waived` in the pull
// request, in Studio and in `glossa findings` — and, until this, not in
// the terminal, which computed its own findings and applied nothing. A
// waiver therefore changed the pull request's counts and not the
// terminal's, which is precisely the disagreement between the two
// surfaces that M4 exists to rule out (RFC 0005 §12.3, §14 decision 1).
//
// Two rules hold here and are what the code is shaped around:
//
//   - **The same matcher.** domain.Waivers decides, here as on the
//     server. A second implementation of "does this waiver cover this
//     finding" would be a second answer, and the two would part on the
//     first revoked waiver, branch scope or moved source revision.
//   - **Never silently.** Offline there is no waiver list to read, and a
//     run that could not read it says so rather than reporting a finding
//     as live that the project has accepted. An unapplied waiver makes
//     the check stricter, not laxer, so it is safe — but it is not
//     honest unless it is said.

// checkWaiversJSON is what the project's waivers did to this run.
type checkWaiversJSON struct {
	// Applied says the project's waivers were read and applied. False
	// with a Why is a run whose findings nobody waived; the counts are
	// then this run's own and not the project's.
	Applied bool `json:"applied"`
	// Live is how many waivers stood when the run read them.
	Live int `json:"live,omitempty"`
	// Waived is how many of this run's findings they accepted.
	Waived int `json:"waived"`
	// Ref is the branch the run graded, which is how far a
	// branch-scoped waiver reached; empty where the run had no branch,
	// and only project-scoped waivers could apply.
	Ref string `json:"ref,omitempty"`
	// Why says why they were not applied; set exactly when Applied is
	// false.
	Why string `json:"why,omitempty"`
}

// readWaivers reads the project's live waivers onto the run.
//
// A waiver list that cannot be read is not a failed check: the run
// still found what it found, and the worst an unread waiver does is
// report an accepted finding as open. So the reason is recorded and the
// run goes on, the way an unreachable termbase leaves its layer named
// rather than stopping the check (RFC 0005 §4.4).
func (inv *invocation) readWaivers(ctx context.Context, run *checkSubject) {
	ws, err := run.client.LiveWaivers(ctx, run.scope)
	if err != nil {
		run.waiversWhy = "the project's waivers couldn't be read: " + asError(err).Error()
		return
	}
	run.waivers = ws
}

// waive applies the project's waivers to what this run found and
// concludes it again, or leaves it alone where there were none to
// apply.
func (s *checkSubject) waive(r qualityapp.Report, policy checkpolicy.Policy) qualityapp.Report {
	return r.Waive(s.waivers, policy, s.ref, time.Now().UTC())
}

// waiversDocument is the run's waiver block, from the report the run
// ended with.
func (s *checkSubject) waiversDocument(r qualityapp.Report) checkWaiversJSON {
	out := checkWaiversJSON{Applied: s.waiversWhy == "", Live: len(s.waivers), Ref: s.ref, Why: s.waiversWhy}
	if !out.Applied {
		return out
	}
	for _, f := range r.Findings {
		if f.Severity == domain.Waived {
			out.Waived++
		}
	}
	return out
}

// printCheckWaivers says what the waivers did, under the findings. A run
// that could not read them says so every time: silence would read as
// "nothing was waived".
func printCheckWaivers(p *printer, w checkWaiversJSON) {
	switch {
	case !w.Applied:
		p.line("  %s", p.dim("the project's waivers were not applied: "+w.Why))
	case w.Waived > 0:
		p.line("  %s", p.dim(plural(w.Waived, "finding", "findings")+" waived by the project"))
	}
}
