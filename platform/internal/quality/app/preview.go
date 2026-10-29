package app

import (
	"sort"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The impact preview of RFC 0005 §4.3: what a candidate policy would
// do, measured against the runs a project already has, before anybody
// saves it.
//
// The kernel answers it for a bag of findings (checkpolicy.Impact).
// This adds the part that needs runs rather than findings: how many
// open pull requests would newly fail, which is the number that decides
// whether a policy ships with a grace.
//
// Nothing here writes. The API that offers it as `dry_run: true` is
// wave 3; Studio shows it before the save and `glossa policy diff`
// prints it.

// PreviewRun is one stored check run the candidate is measured against:
// its findings, and enough about it to say what would happen to it.
type PreviewRun struct {
	// Ref is the branch or environment the run was for.
	Ref string
	// Environment is the environment the run graded in, "" for a branch.
	Environment string
	// PullRequest is the pull request the run belongs to, 0 for a run
	// that is not one's.
	PullRequest int
	// Open says the pull request is still open, which is what makes it
	// something a policy change can break.
	Open bool
	// Findings are the run's findings as its layers emitted them. A
	// waived finding is passed through: it cannot fail a run under any
	// policy, so a candidate cannot change what it does.
	Findings []domain.Finding
}

// Preview is the answer: the kernel's per-finding and per-rule impact,
// plus what it means for the runs and the pull requests that exist.
type Preview struct {
	checkpolicy.ImpactReport
	// Runs is how many runs the preview was measured against.
	Runs int
	// NewlyFailingRefs are the refs whose verdict turns from passing to
	// failing under the candidate, in order.
	NewlyFailingRefs []string
	// NoLongerFailingRefs are the refs that stop failing.
	NoLongerFailingRefs []string
	// OpenPullRequests counts the open pull requests among NewlyFailingRefs.
	// It is the number §4.3 asks the preview to show, because it is the
	// number of people who would wake up to a red pull request they did
	// not cause.
	OpenPullRequests int
	// PullRequests names them: each open pull request among
	// NewlyFailingRefs, in ref order. Its length is OpenPullRequests. A
	// count says how many people would wake up to a red pull request; a
	// name is what lets whoever saves the policy go and tell them.
	PullRequests []PullRequestImpact
}

// PullRequestImpact is one open pull request a candidate policy would
// newly fail.
type PullRequestImpact struct {
	// Ref is the pull request's branch.
	Ref string
	// Number is the pull request's number on its repository.
	Number int
	// URL is where it is on the web, "" when the project has no
	// repository to point at (or the caller may not read the
	// integration that knows).
	URL string
}

// PreviewPolicy answers what candidate would do to runs, against what
// current decides about them today. It stores nothing and changes
// neither document.
func PreviewPolicy(current, candidate checkpolicy.Policy, runs []PreviewRun) Preview {
	out := Preview{Runs: len(runs)}
	var targets []checkpolicy.Target
	newly, no := map[string]bool{}, map[string]bool{}
	pullRequests := map[int]string{}
	for _, r := range runs {
		for _, f := range r.Findings {
			if f.Severity == domain.Waived {
				continue
			}
			targets = append(targets, f.Target(r.Environment))
		}
		was := domain.Evaluate(current, r.Environment, r.Findings)
		now := domain.Evaluate(candidate, r.Environment, r.Findings)
		switch {
		case was.Passed() && !now.Passed():
			newly[r.Ref] = true
			if r.Open && r.PullRequest > 0 {
				pullRequests[r.PullRequest] = r.Ref
			}
		case !was.Passed() && now.Passed():
			no[r.Ref] = true
		}
	}
	out.ImpactReport = checkpolicy.Impact(current, candidate, targets)
	out.NewlyFailingRefs, out.NoLongerFailingRefs = sortedKeys(newly), sortedKeys(no)
	out.OpenPullRequests = len(pullRequests)
	for n, ref := range pullRequests {
		out.PullRequests = append(out.PullRequests, PullRequestImpact{Ref: ref, Number: n})
	}
	sort.Slice(out.PullRequests, func(i, j int) bool {
		a, b := out.PullRequests[i], out.PullRequests[j]
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Number < b.Number
	})
	return out
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
