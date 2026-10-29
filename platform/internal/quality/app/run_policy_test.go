package app_test

import (
	"slices"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// A run under the policy document (RFC 0005 §4): the policy decides
// which layers run, what each finding is worth here, and what fails.

func TestRunSkipsALayerAPolicySwitchedOff(t *testing.T) {
	p := checkpolicy.Policy{Version: 7, Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "parity"}, Severity: checkpolicy.Off},
	}}
	r := app.Run(project(t), p, layers.Default()...)
	if slices.Contains(r.Layers, domain.LayerParity) {
		t.Error("the parity layer ran, want a project not to pay for a layer it switched off")
	}
	if !slices.Contains(r.Skipped, domain.LayerParity) {
		t.Errorf("skipped = %v, want the layer named rather than silently dropped", r.Skipped)
	}
	for _, f := range r.Findings {
		if f.Layer == domain.LayerParity {
			t.Errorf("finding %q from a layer that did not run", f.Code)
		}
	}
	if r.PolicyVersion != 7 {
		t.Errorf("policy version = %d, want the document's 7", r.PolicyVersion)
	}
	if len(r.Decisions) != len(r.Findings) {
		t.Errorf("decisions = %d for %d findings, want one per finding for --explain-policy",
			len(r.Decisions), len(r.Findings))
	}
}

func TestRunGradesFindingsThroughTheRules(t *testing.T) {
	// The Japanese translation of checkout.pay is missing, which the
	// completeness layer makes an error; a rule makes it a warning in ja
	// alone.
	p := checkpolicy.Policy{Version: 2, Rules: []checkpolicy.Rule{{
		Selector: checkpolicy.Selector{Layer: "completeness", Locale: "ja"}, Severity: checkpolicy.Warning,
	}}}
	r := app.Run(project(t), p, layers.Default()...)
	for i, f := range r.Findings {
		if f.Locus.Locale != "ja" || f.Layer != domain.LayerCompleteness {
			continue
		}
		if f.Severity != domain.Warning {
			t.Errorf("%s in ja = %q, want the rule's warning", f.Code, f.Severity)
		}
		if r.Decisions[i].Rule != 0 {
			t.Errorf("%s in ja was decided by rule %d, want rule 0", f.Code, r.Decisions[i].Rule)
		}
	}
	// The same finding in de keeps the layer's error: the rule named a
	// locale, and de is not it.
	for i, f := range r.Findings {
		if f.Locus.Locale == "de" && f.Layer == domain.LayerCompleteness && r.Decisions[i].Rule != -1 {
			t.Errorf("%s in de was decided by rule %d, want no rule to reach it", f.Code, r.Decisions[i].Rule)
		}
	}
}

func TestRunInReadsTheEnvironmentsRequireComplete(t *testing.T) {
	p := checkpolicy.Policy{
		Version: 3, RequireComplete: []string{"de"},
		Environments: map[string]checkpolicy.Environment{
			"production": {RequireComplete: checkpolicy.RequiredLocales("de", "ja")},
		},
	}
	branch := app.RunIn(project(t), p, "", layers.Default()...)
	production := app.RunIn(project(t), p, "production", layers.Default()...)
	required := func(r app.Report, locale string) bool {
		for _, l := range r.Locales {
			if l.Code == locale {
				return l.Required
			}
		}
		t.Fatalf("no report for %q", locale)
		return false
	}
	if required(branch, "ja") {
		t.Error("ja is required on a branch, want the document's own require_complete")
	}
	if !required(production, "ja") {
		t.Error("ja is not required in production, want the environment's require_complete")
	}
	// And the severity follows: an untranslated key in a locale the
	// environment requires is an error there and a warning on a branch.
	severity := func(r app.Report) domain.Severity {
		for _, f := range r.Findings {
			if f.Locus.Locale == "ja" && f.Code == checkpolicy.CodeMissingTranslation {
				return f.Severity
			}
		}
		t.Fatal("no missing-translation finding for ja")
		return ""
	}
	if got := severity(branch); got != domain.Warning {
		t.Errorf("ja on the branch = %q, want a warning", got)
	}
	if got := severity(production); got != domain.Error {
		t.Errorf("ja in production = %q, want an error", got)
	}
}
