package checkpolicy

// The impact preview (RFC 0005 §4.3): what a candidate document would
// do, measured against findings that already exist, before anyone
// saves it. Studio shows it on the policy editor and `glossa policy
// diff` prints it; the API that offers it as `dry_run: true` is wave 3.
//
// It is a pure comparison of two documents over a set of targets, so
// the same function answers for one project's stored findings, for a
// single pull request, or for a test's table.

// rank orders the severities a decision can carry, so a change can say
// whether it raised or lowered the bar. Off is below Warning: a rule
// that switches a layer off silences it.
func rank(s Severity) int {
	switch s {
	case Off:
		return 0
	case Warning:
		return 1
	case Error:
		return 2
	}
	return 1
}

// Change is one target the candidate decides differently.
type Change struct {
	Target Target
	From   Severity
	To     Severity
	// FromRule and ToRule are the rules that decided, or -1 where the
	// layer's own severity stood.
	FromRule int
	ToRule   int
	// WasFailing and Fails say what the two documents' verdicts on this
	// one finding are — which is not the same question as the severity,
	// because a rule in warn mode reports without failing.
	WasFailing bool
	Fails      bool
}

// Raised reports whether the candidate made this finding count for more.
func (c Change) Raised() bool { return rank(c.To) > rank(c.From) }

// Silenced reports whether the candidate stopped computing it.
func (c Change) Silenced() bool { return c.To == Off && c.From != Off }

// RuleImpact is what one rule of the candidate did.
type RuleImpact struct {
	// Rule is the rule's index in the candidate's Rules.
	Rule     int
	Selector Selector
	Severity Severity
	Mode     Mode
	// Matched counts the targets this rule decided; Changed counts those
	// it decided differently from the current document, and NewlyFailing
	// those it turned into a reason the run fails.
	Matched      int
	Changed      int
	NewlyFailing int
}

// ImpactReport is the preview: how many findings change severity, and
// how many of them newly fail, per rule (RFC 0005 §4.3).
type ImpactReport struct {
	// Targets is how many findings the preview was measured against.
	Targets int
	Changes []Change
	// Raised, Lowered and Silenced count the changes by direction.
	Raised   int
	Lowered  int
	Silenced int
	// NewlyFailing counts findings that fail under the candidate and did
	// not under the current document; NoLongerFailing the reverse.
	NewlyFailing    int
	NoLongerFailing int
	// Rules is one entry per rule of the candidate, in document order,
	// including the rules nothing matched — a rule that changes nothing
	// is exactly what a reader wants to know before saving.
	Rules []RuleImpact
}

// Impact previews what candidate would decide about targets that
// current decides about today. Neither document is stored, read or
// changed.
func Impact(current, candidate Policy, targets []Target) ImpactReport {
	out := ImpactReport{Targets: len(targets)}
	out.Rules = make([]RuleImpact, 0, len(candidate.Rules))
	for i, r := range candidate.Rules {
		out.Rules = append(out.Rules, RuleImpact{
			Rule: i, Selector: r.Selector, Severity: r.Severity, Mode: r.mode(),
		})
	}
	for _, t := range targets {
		was := current.Decide(t)
		now := candidate.Decide(t)
		wasFailing, fails := current.FailsDecision(was), candidate.FailsDecision(now)
		if now.Rule >= 0 {
			out.Rules[now.Rule].Matched++
		}
		if was.Severity == now.Severity && wasFailing == fails {
			continue
		}
		c := Change{
			Target: t, From: was.Severity, To: now.Severity,
			FromRule: was.Rule, ToRule: now.Rule, WasFailing: wasFailing, Fails: fails,
		}
		out.Changes = append(out.Changes, c)
		switch {
		case c.Silenced():
			out.Silenced++
		case c.Raised():
			out.Raised++
		case rank(c.To) < rank(c.From):
			out.Lowered++
		}
		if fails && !wasFailing {
			out.NewlyFailing++
		}
		if wasFailing && !fails {
			out.NoLongerFailing++
		}
		if now.Rule >= 0 {
			out.Rules[now.Rule].Changed++
			if fails && !wasFailing {
				out.Rules[now.Rule].NewlyFailing++
			}
		}
	}
	return out
}
