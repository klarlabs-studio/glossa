package cli

import (
	"strings"
	"testing"
)

const styleYAML = `name: Shop German
fields:
  formality: {register: informal, pronoun: du}
  tone: [friendly, concise]
  punctuation: {quotes: "„“"}
rules:
  - id: no-exclamation
    title: No exclamation marks
    rationale: They read as shouting.
    good: ["Gespeichert."]
    bad: ["Gespeichert!"]
`

func TestStyleEditCreatesThenUpdatesAndShowMerges(t *testing.T) {
	_, w := seeded(t)
	w.write("style/tenant.yaml", "fields:\n  tone: [neutral]\n  numbers: {decimal_separator: \".\"}\n")
	w.write("style/de.yaml", styleYAML)

	var out styleEditJSON
	w.json(&out, "style", "edit", "--file", "style/tenant.yaml", "--tenant-wide").want(t, ExitOK)
	if out.Schema != "glossa.cli.style.edit/v1" || out.Action != "created" || out.Guide.Scope.ProjectID != nil || out.Guide.Scope.Locale != nil {
		t.Fatalf("tenant guide = %+v", out)
	}
	w.json(&out, "style", "edit", "--file", "style/de.yaml", "--locale", "de").want(t, ExitOK)
	g := out.Guide
	if out.Action != "created" || g.Name != "Shop German" || g.Scope.ProjectID == nil || *g.Scope.Locale != "de" ||
		g.Fields.Formality == nil || *g.Fields.Formality.Pronoun != "du" || len(g.Rules) != 1 || g.Rules[0].Id != "no-exclamation" {
		t.Fatalf("de guide = %+v", out)
	}
	w.json(&out, "style", "edit", "--file", "style/de.yaml", "--locale", "de").want(t, ExitOK)
	if out.Action != "unchanged" || out.Guide.ID != g.ID || out.Guide.Version != 1 {
		t.Errorf("same file again = %+v", out)
	}
	w.write("style/de.yaml", strings.Replace(styleYAML, "concise", "warm", 1))
	w.json(&out, "style", "edit", "--file", "style/de.yaml", "--locale", "de").want(t, ExitOK)
	if out.Action != "updated" || out.Guide.ID != g.ID || out.Guide.Version != 2 {
		t.Errorf("changed file = %+v", out)
	}

	var show styleShowJSON
	w.json(&show, "style", "show", "--locale", "de").want(t, ExitOK)
	if show.Schema != "glossa.cli.style.show/v1" || len(show.Sources) != 2 || show.Fields.Numbers == nil ||
		show.Fields.Formality == nil || strings.Join(*show.Fields.Tone, ",") != "friendly,warm" || len(show.Rules) != 1 {
		t.Fatalf("show de = %+v", show)
	}
	var tenantOnly styleShowJSON
	w.json(&tenantOnly, "style", "show").want(t, ExitOK)
	if len(tenantOnly.Sources) != 1 || tenantOnly.Fields.Formality != nil {
		t.Errorf("show without locale = %+v", tenantOnly)
	}
	h := w.run("style", "show", "--locale", "de")
	for _, want := range []string{"Style for shop · de", "formality.pronoun", "du", "no-exclamation", "bad:", "from: tenant v1 → de v2"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human show lacks %q:\n%s", want, h.stdout)
		}
	}
	if h := w.run("style", "show", "--locale", "ja", "--tenant"); !strings.Contains(h.stdout, "Style for the tenant · ja") {
		t.Errorf("tenant show:\n%s", h.stdout)
	}
}

func TestStyleEditRejectsBadFiles(t *testing.T) {
	_, w := seeded(t)
	w.write("bad.yaml", "fields:\n  formallity: {register: formal}\n")
	w.write("empty.yaml", "# nothing\n{}\n")
	w.write("broken.yaml", "fields: [\n")
	for _, c := range []struct{ file, code string }{
		{"bad.yaml", "invalid_style_file"}, {"empty.yaml", "invalid_style_file"}, {"broken.yaml", "invalid_style_file"}, {"missing.yaml", "file_not_found"},
	} {
		var e errorDoc
		w.json(&e, "style", "edit", "--file", c.file).want(t, ExitUsage)
		if e.Error.Code != c.code || e.Error.Where == "" {
			t.Errorf("%s: %+v", c.file, e)
		}
	}
	w.write("ns.yaml", styleYAML)
	var e errorDoc
	w.json(&e, "style", "edit", "--file", "ns.yaml", "--namespace", "checkout", "--tenant-wide").want(t, ExitUsage)
	w.run("style", "edit").want(t, ExitUsage)
	w.run("style").want(t, ExitUsage)
	w.run("style", "delete").want(t, ExitUsage)
	w.run("style", "show", "--locale", "nöt").want(t, ExitUsage)
}
