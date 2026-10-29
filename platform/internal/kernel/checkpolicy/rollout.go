package checkpolicy

import "time"

// Rolling out a stricter policy (RFC 0005 §4.3).
//
// The failure mode is obvious and avoidable: someone adds
// `terminology: error`, and forty open pull requests go red for
// something their authors did not do. Three mechanisms answer it, and
// they are deliberately separate:
//
//   - Mode: a rule can ship as ModeWarn, computing and reporting but
//     never changing a conclusion. That is the on-ramp for one rule.
//   - Version: every document has one, and every run records the
//     version it used, so "why did this fail?" can name the policy as
//     well as the rule.
//   - Grace: a version saved with a GraceUntil pins pull requests
//     *older than it* to the version they were opened under, until the
//     grace ends. That is the on-ramp for a whole document.
//
// Nothing here reads a clock. The caller hands in both times, because a
// check must grade the same way on a re-run as it did on the run.

// DefaultGrace is how long a saved policy pins the pull requests that
// predate it, when the save asks for a grace without saying how long
// (RFC 0005 §4.3).
const DefaultGrace = 14 * 24 * time.Hour

// Supersede returns p as the version that replaces current: its version
// one past current's, the moment it takes effect recorded, and current
// kept as the version the pull requests opened before that moment keep
// grading against until the grace ends.
//
// A zero grace pins nothing: the new version grades every pull request
// at once, which is what a policy that only loosens wants.
func (p Policy) Supersede(current Policy, now time.Time, grace time.Duration) Policy {
	p.Schema = Schema
	p.Version = current.Version + 1
	effective := now.UTC()
	p.EffectiveFrom = &effective
	p.GraceUntil, p.Previous = nil, nil
	if grace <= 0 {
		return p
	}
	until := effective.Add(grace)
	p.GraceUntil = &until
	// One version deep: what the superseded document kept behind it is
	// already out of grace, or its grace ends before this one's.
	previous := current.Current()
	p.Previous = &previous
	return p
}

// Current is the document without its history: the version itself, as a
// check grades against it.
func (p Policy) Current() Policy {
	p.Previous = nil
	return p
}

// Effective is the version of the document that grades a run.
//
// openedAt is when the pull request was opened — the zero time for a
// run that is not a pull request's, or one whose age nobody recorded,
// and then the current version applies. now is when the run happens.
//
// A pull request is pinned when all four hold: the document keeps a
// previous version, the save asked for a grace, the grace has not run
// out, and the pull request predates the save. Otherwise the current
// version grades it. Merging is never blocked by a rule the branch
// predates, and no pull request is pinned forever.
func (p Policy) Effective(openedAt, now time.Time) Policy {
	if !p.Pins(openedAt, now) {
		return p.Current()
	}
	return p.Previous.Current()
}

// Pins reports whether the grace pins a pull request opened at openedAt
// to the previous version, at now.
func (p Policy) Pins(openedAt, now time.Time) bool {
	if p.Previous == nil || p.GraceUntil == nil || p.EffectiveFrom == nil || openedAt.IsZero() {
		return false
	}
	return openedAt.Before(*p.EffectiveFrom) && now.Before(*p.GraceUntil)
}
