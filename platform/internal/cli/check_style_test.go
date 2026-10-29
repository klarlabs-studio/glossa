package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The style layer grades a translation against the project's effective
// style guide (RFC 0005 §3.2). Only the server can resolve that guide,
// so `glossa check` has to fetch it — and where it cannot, it has to say
// so rather than let a layer that never ran read as one that passed
// (intent §41).
//
// The three answers this file pins are different on purpose:
//
//   - a guide that states a mechanical rule: the layer grades against it;
//   - a guide that states none: the layer ran and found nothing, which
//     is a real answer about this locale and not a gap;
//   - no server to ask: the layer could not run, is named in
//     `unavailable_layers`, and the run exits 4.

// styleGuide gives the project a guide for locale.
func styleGuide(srv *fakeServer, locale string, fields map[string]any) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	project, l := "prj_1", locale
	srv.kn.guides = append(srv.kn.guides, &fakeGuide{
		id: srv.kn.nextID(), name: "Brotwerk", project: &project, locale: &l, version: 1,
		fields: fields, rules: []any{},
	})
}

// styled is a project with one German translation addressing the reader
// as `du`.
func styled(t *testing.T) (*fakeServer, *workspace) {
	t.Helper()
	srv := newFakeServer(t)
	srv.locales = []string{"de", "en"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{"home.greeting": "Welcome back"}`})
	w.run("push").want(t, ExitOK)
	w.write("locales/de.json", `{"home.greeting": "Willkommen zurück, du"}`)
	w.run("push", "--translations").want(t, ExitOK)
	return srv, w
}

func styleFindings(doc checkJSON) []domain.Finding {
	var out []domain.Finding
	for _, f := range doc.Findings {
		if f.Layer == domain.LayerStyle {
			out = append(out, f)
		}
	}
	return out
}

// TestCheckGradesAgainstTheEffectiveStyleGuide: the guide asks for the
// formal form of address and the translation uses the informal one, so
// the terminal reports it. Before this, `cli/qa.Project` built a
// `layers.Project` with no `Styles` map at all, and `layers.Style.Check`
// returned before it read a word.
func TestCheckGradesAgainstTheEffectiveStyleGuide(t *testing.T) {
	srv, w := styled(t)
	styleGuide(srv, "de", map[string]any{
		"formality": map[string]any{"register": "formal", "pronoun": "Sie"},
	})
	var doc checkJSON
	w.json(&doc, "check", "--require-complete=none").want(t, ExitOK)
	fs := styleFindings(doc)
	if len(fs) != 1 || fs[0].Code != "formality-mismatch" || fs[0].Locus.Locale != "de" {
		t.Fatalf("style findings = %+v", fs)
	}
	if fs[0].Subject != "du" || !strings.Contains(fs[0].Message, "Sie") {
		t.Errorf("finding = %+v, want the run the translation wrote and the form the guide asks for", fs[0])
	}
	// The layer ran, so it is named, and nothing is missing.
	if !hasLayer(doc.Layers, domain.LayerStyle) || len(doc.Unavailable) != 0 {
		t.Errorf("layers = %v, unavailable = %+v", doc.Layers, doc.Unavailable)
	}
	// The guide's version is evidence: a reader can tell which document
	// graded the text.
	if fs[0].Evidence["style_guide"] == nil {
		t.Errorf("evidence = %+v, want the guide it graded against", fs[0].Evidence)
	}
}

// TestALocaleWithNoMechanicalGuideIsARealAnswer: the layer ran, had
// nothing to check this locale against, and said nothing. That is not a
// gap, and the run must not report one — otherwise every project
// without a style guide would exit 4 forever.
func TestALocaleWithNoMechanicalGuideIsARealAnswer(t *testing.T) {
	srv, w := styled(t)
	// A guide of nothing but prose states no mechanical rule (§3.2): it
	// is prompt material and evidence, never something a regex grades.
	styleGuide(srv, "de", map[string]any{"tone": []string{"warm", "concise"}})
	var doc checkJSON
	w.json(&doc, "check", "--require-complete=none").want(t, ExitOK)
	if fs := styleFindings(doc); len(fs) != 0 {
		t.Errorf("a guide with no mechanical rule graded something: %+v", fs)
	}
	if !hasLayer(doc.Layers, domain.LayerStyle) {
		t.Errorf("layers = %v, want the layer named: it ran", doc.Layers)
	}
	if len(doc.Unavailable) != 0 {
		t.Errorf("unavailable = %+v: no guide is an answer, not a gap", doc.Unavailable)
	}
	// And with no guide at all, the same.
	srv2, w2 := styled(t)
	_ = srv2
	var none checkJSON
	w2.json(&none, "check", "--require-complete=none").want(t, ExitOK)
	if len(styleFindings(none)) != 0 || !hasLayer(none.Layers, domain.LayerStyle) || len(none.Unavailable) != 0 {
		t.Errorf("no guide: findings = %+v, layers = %v, unavailable = %+v",
			styleFindings(none), none.Layers, none.Unavailable)
	}
}

// TestOfflineCannotComputeTheStyleLayer is the other half: offline the
// effective style guide cannot be resolved at all, so the layer is named
// as one this run could not compute and the run exits 4. Reporting
// nothing and passing would be a check claiming to have graded style it
// never read.
func TestOfflineCannotComputeTheStyleLayer(t *testing.T) {
	srv, w := styled(t)
	styleGuide(srv, "de", map[string]any{
		"formality": map[string]any{"register": "formal", "pronoun": "Sie"},
	})
	var doc checkJSON
	w.json(&doc, "check", "--offline", "--require-complete=none").want(t, ExitPartial)
	if len(doc.Unavailable) != 1 || doc.Unavailable[0].Layer != domain.LayerStyle {
		t.Fatalf("unavailable = %+v", doc.Unavailable)
	}
	if doc.Unavailable[0].Why == "" {
		t.Error("an unavailable layer has to say why")
	}
	// Not "clean": the layer is not among the ones that ran.
	if hasLayer(doc.Layers, domain.LayerStyle) {
		t.Errorf("layers = %v, want style left out: it could not run", doc.Layers)
	}
	if !doc.Passed {
		t.Errorf("a layer that could not run is not a failed check: %+v", doc)
	}
	h := w.run("check", "--offline", "--require-complete=none")
	h.want(t, ExitPartial)
	if !strings.Contains(h.stdout, "style not checked") {
		t.Errorf("the human output doesn't name the layer:\n%s", h.stdout)
	}
}

// TestALayerNobodyAskedForIsNotAGap: `--layer` left style out, so it is
// deselected, not unavailable. A run that was never going to compute the
// layer has lost nothing.
func TestALayerNobodyAskedForIsNotAGap(t *testing.T) {
	_, w := styled(t)
	var doc checkJSON
	w.json(&doc, "check", "--offline", "--layer", "completeness").want(t, ExitOK)
	if len(doc.Unavailable) != 0 {
		t.Errorf("unavailable = %+v, want none: --layer left style out", doc.Unavailable)
	}
}

// TestTheStyleGuidesAreReadOncePerTargetLocale: one request per target
// locale and none for the source, which has no translations to grade.
func TestTheStyleGuidesAreReadOncePerTargetLocale(t *testing.T) {
	srv, w := styled(t)
	styleGuide(srv, "de", map[string]any{
		"formality": map[string]any{"register": "formal", "pronoun": "Sie"},
	})
	w.run("check", "--require-complete=none").want(t, ExitOK)
	if n := srv.countRequests("GET /v1/tenants/ten_1/effective-style-guide"); n != 1 {
		t.Errorf("effective-style-guide read %d times, want one per target locale", n)
	}
}
