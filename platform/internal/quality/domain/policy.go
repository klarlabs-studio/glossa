package domain

import (
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// Grading findings against the policy document (RFC 0005 §4).
//
// A layer says what is wrong and how bad it looks from where the layer
// stands. The policy says what that is worth *here* — in this locale,
// this namespace, this environment — and whether the run fails over it.
// The two are deliberately separate: a layer that graded itself could
// not be given a different answer per project, and a policy that found
// things would be a second implementation of the layer.
//
// Everything below is the same evaluator on both surfaces. `glossa
// check` and the pull-request check hand the same findings and the same
// document to Evaluate and get the same verdict, which is the one
// property checkpolicy was created to protect.

// Target is the finding as the policy's selectors see it.
//
// environment is the environment the run is about, and is "" for a
// branch check — which is every check in CI. A rule that names an
// environment therefore says nothing about a pull request.
func (f Finding) Target(environment string) checkpolicy.Target {
	return checkpolicy.Target{
		Layer: string(f.Layer), Code: f.Code, Locale: f.Locus.Locale,
		Namespace: f.Locus.Namespace, Environment: environment,
		Severity: f.Severity, Advisory: f.Layer.Advisory(), Provisional: f.Provisional(),
	}
}

// Graded is a finding with the decision the policy took about it, which
// is what `glossa check --explain-policy` prints: "why did this fail?"
// has a mechanical answer.
type Graded struct {
	Finding
	Decision checkpolicy.Decision
}

// Evaluation is one run graded: the findings as the policy leaves them,
// what they add up to, and the verdict.
type Evaluation struct {
	Graded []Graded
	Counts Counts
	// Conclusion is the verdict. A finding a rule switched off is not in
	// it because it was never computed; a finding a rule left in warn
	// mode is in the report and not in the verdict.
	Conclusion Conclusion
	// PolicyVersion is the version of the document that graded this run,
	// so a run can say which it used when two are live at once
	// (RFC 0005 §4.3). 0 is a policy that has no versions, which is
	// every policy stored before M4.
	PolicyVersion int
}

// Findings are the graded findings, for a caller that wants the
// findings and not the decisions.
func (e Evaluation) Findings() []Finding {
	out := make([]Finding, 0, len(e.Graded))
	for _, g := range e.Graded {
		out = append(out, g.Finding)
	}
	return out
}

// Passed reports whether the run's conclusion lets a build through.
func (e Evaluation) Passed() bool { return e.Conclusion != ConclusionFailure }

// Evaluate grades fs against the policy document in environment env.
//
// Each finding's severity becomes the one the policy decides; a finding
// a rule switched off is dropped, because `off` means the project does
// not compute it and a finding nobody computed cannot be reported; a
// rule in warn mode counts at its severity and cannot change the
// conclusion. A waived finding is passed through untouched: it is
// already accepted, still reported, counted on its own, and can never
// fail a run (RFC 0005 §2.3).
//
// A document with no rules decides exactly what the three fields
// decided before M4 — no rule matches, every finding keeps the severity
// its layer gave it, and the conclusion is fail_on over those — which
// is why migrating a project's stored policy cannot move its verdict.
func Evaluate(p checkpolicy.Policy, env string, fs []Finding) Evaluation {
	out := Evaluation{Conclusion: ConclusionSuccess, PolicyVersion: p.Version}
	for _, f := range fs {
		if f.Severity == Waived {
			out.Graded = append(out.Graded, Graded{
				Finding:  f,
				Decision: checkpolicy.Decision{Severity: Waived, Mode: checkpolicy.ModeEnforce, Rule: -1},
			})
			out.Counts.Count(f)
			continue
		}
		d := p.Decide(f.Target(env))
		if !d.Computed() {
			continue
		}
		f.Severity = d.Severity
		out.Graded = append(out.Graded, Graded{Finding: f, Decision: d})
		out.Counts.Count(f)
		if p.FailsDecision(d) {
			out.Conclusion = ConclusionFailure
		}
	}
	return out
}

// Computes reports whether the policy runs the layer at all in env. A
// run asks before it computes, so a layer a project switched off costs
// it nothing, and so the run's layer list tells "clean" from "not
// looked at".
func Computes(p checkpolicy.Policy, env string, l Layer) bool {
	return p.Computes(string(l), env)
}
