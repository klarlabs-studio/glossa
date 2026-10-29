package domain_test

import (
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// TestLayersMirrorTheKernel: the policy has to be able to refuse a rule
// naming a layer that does not exist, so the kernel owns the list. This
// holds the two spellings together.
func TestLayersMirrorTheKernel(t *testing.T) {
	var ours []string
	for _, l := range domain.Layers {
		ours = append(ours, string(l))
	}
	if !slices.Equal(ours, checkpolicy.Layers) {
		t.Errorf("quality layers = %v, kernel layers = %v: a rule could name a layer that does not exist,"+
			" or a layer no rule could name", ours, checkpolicy.Layers)
	}
	for _, l := range domain.Layers {
		if l.Advisory() != checkpolicy.Advisory(string(l)) {
			t.Errorf("%s: advisory = %v here and %v in the kernel", l, l.Advisory(), checkpolicy.Advisory(string(l)))
		}
	}
	if !domain.LayerLinguistic.Advisory() {
		t.Error("the linguistic layer is not advisory, want a build that never fails on an opinion")
	}
}

func graded(layer domain.Layer, code, locale, ns string, sev domain.Severity) domain.Finding {
	return domain.New(domain.Finding{
		Layer: layer, Code: code, Severity: sev,
		Locus:   domain.Locus{Key: "checkout.pay", Locale: locale, Namespace: ns},
		Message: code,
	})
}

func fixture() []domain.Finding {
	return []domain.Finding{
		graded(domain.LayerParity, "missing-argument", "de", "checkout", domain.Error),
		graded(domain.LayerTerminology, "term_forbidden", "de", "legal", domain.Warning),
		graded(domain.LayerSource, "manual-plural", "en", "checkout", domain.Warning),
		graded(domain.LayerLinguistic, "tone-mismatch", "de", "legal", domain.Warning),
	}
}

// TestEvaluateWithoutRulesIsWhatTheThreeFieldsDid: the migration of the
// stored policy cannot move a project's verdict, because a document
// with no rules grades exactly as fail_on over the layers' own
// severities did.
func TestEvaluateWithoutRulesIsWhatTheThreeFieldsDid(t *testing.T) {
	for _, p := range []checkpolicy.Policy{
		{},
		{FailOn: checkpolicy.Warning},
		{FailOn: checkpolicy.Never},
		{RequireComplete: []string{"de"}, MissingTranslations: checkpolicy.Warning},
	} {
		fs := fixture()
		ev := domain.Evaluate(p, "", fs)
		counts, conclusion := domain.Conclude(p, fs)
		if ev.Counts != counts || ev.Conclusion != conclusion {
			t.Errorf("policy %+v: Evaluate = %v/%q, Conclude = %v/%q",
				p, ev.Counts, ev.Conclusion, counts, conclusion)
		}
		for i, g := range ev.Graded {
			if g.Severity != fs[i].Severity || g.Decision.Rule != -1 {
				t.Errorf("policy %+v: finding %d = %+v, want the layer's own severity and no rule", p, i, g)
			}
		}
	}
}

func TestEvaluate(t *testing.T) {
	p := checkpolicy.Policy{
		Version: 7,
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "source"}, Severity: checkpolicy.Off},
			{Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, Severity: checkpolicy.Error},
		},
	}
	ev := domain.Evaluate(p, "", fixture())
	if ev.PolicyVersion != 7 {
		t.Errorf("policy version = %d, want the document's 7", ev.PolicyVersion)
	}
	if len(ev.Graded) != 3 {
		t.Fatalf("graded %d findings, want the one a rule switched off dropped", len(ev.Graded))
	}
	for _, g := range ev.Graded {
		if g.Layer == domain.LayerSource {
			t.Error("a finding a rule switched off reached the report")
		}
		if g.Layer == domain.LayerTerminology {
			if g.Severity != domain.Error || g.Decision.Rule != 1 {
				t.Errorf("terminology = %+v, want rule 1's error", g)
			}
		}
	}
	if ev.Counts.Errors != 2 || ev.Counts.Warnings != 1 || ev.Conclusion != domain.ConclusionFailure {
		t.Errorf("counts = %+v, conclusion = %q", ev.Counts, ev.Conclusion)
	}
}

func TestEvaluateWarnModeReportsWithoutFailing(t *testing.T) {
	warn := checkpolicy.Policy{Version: 8, Rules: []checkpolicy.Rule{{
		Selector: checkpolicy.Selector{Layer: "terminology"},
		Severity: checkpolicy.Error, Mode: checkpolicy.ModeWarn,
	}}}
	ev := domain.Evaluate(warn, "", fixture()[1:2])
	if len(ev.Graded) != 1 || ev.Graded[0].Severity != domain.Error {
		t.Fatalf("graded = %+v, want the finding reported at the rule's severity", ev.Graded)
	}
	if ev.Counts.Errors != 1 {
		t.Errorf("errors = %d, want the number to be true", ev.Counts.Errors)
	}
	if ev.Conclusion != domain.ConclusionSuccess {
		t.Errorf("conclusion = %q, want warn mode never to change it", ev.Conclusion)
	}
	// The same rule enforcing is the point of the on-ramp.
	warn.Rules[0].Mode = checkpolicy.ModeEnforce
	if ev := domain.Evaluate(warn, "", fixture()[1:2]); ev.Conclusion != domain.ConclusionFailure {
		t.Errorf("conclusion = %q once enforced, want failure", ev.Conclusion)
	}
}

func TestEvaluateLeavesWaivedFindingsAlone(t *testing.T) {
	waived := graded(domain.LayerTerminology, "term_forbidden", "de", "legal", domain.Warning).Waive("w_1")
	p := checkpolicy.Policy{
		FailOn: checkpolicy.Warning,
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error},
		},
	}
	ev := domain.Evaluate(p, "", []domain.Finding{waived})
	if len(ev.Graded) != 1 || ev.Graded[0].Severity != domain.Waived {
		t.Fatalf("graded = %+v, want the waived finding still reported, as waived", ev.Graded)
	}
	if ev.Counts.Waived != 1 || ev.Counts.Errors != 0 {
		t.Errorf("counts = %+v, want a waived finding counted on its own", ev.Counts)
	}
	if ev.Conclusion != domain.ConclusionSuccess {
		t.Errorf("conclusion = %q, want a waived finding never to fail a run", ev.Conclusion)
	}
}

func TestEvaluatePerEnvironment(t *testing.T) {
	p := checkpolicy.Policy{Rules: []checkpolicy.Rule{{
		Selector: checkpolicy.Selector{Layer: "terminology", Environment: "production"},
		Severity: checkpolicy.Error,
	}}}
	fs := fixture()[1:2]
	if ev := domain.Evaluate(p, "", fs); ev.Conclusion != domain.ConclusionSuccess {
		t.Errorf("branch conclusion = %q, want a rule about production to say nothing here", ev.Conclusion)
	}
	if ev := domain.Evaluate(p, "production", fs); ev.Conclusion != domain.ConclusionFailure {
		t.Errorf("production conclusion = %q, want the rule to bite there", ev.Conclusion)
	}
}

func TestComputes(t *testing.T) {
	p := checkpolicy.Policy{Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "source"}, Severity: checkpolicy.Off},
		{Selector: checkpolicy.Selector{Layer: "length", Locale: "ja"}, Severity: checkpolicy.Off},
	}}
	if domain.Computes(p, "", domain.LayerSource) {
		t.Error("the source layer computes, want a project not to pay for a layer it switched off")
	}
	if !domain.Computes(p, "", domain.LayerLength) {
		t.Error("the length layer does not compute, want a rule about one locale not to switch off a layer")
	}
}
