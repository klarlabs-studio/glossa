package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

// v0.3's text: two keys with bare apostrophes (v0.3's known defect),
// one quoted correctly, a plural, a select, and one whose platform text
// will differ in a word as well as by the apostrophe.
var verifyV03 = map[string]map[string]string{
	"de": {
		"copy.bare":     "Geht's gut, {name}?",
		"copy.rock":     "Rock'n'roll mit {name}",
		"copy.escaped":  "Das ist {name}s ''Wahl''",
		"cart.items":    "{count, plural, =0 {Keine Artikel} one {# Artikel} other {# Artikel}}",
		"activity.who":  "{gender, select, female {Sie hat geantwortet} other {Jemand hat geantwortet}}",
		"copy.reworded": "Wie geht's, {name}?",
	},
	"en": {
		"copy.bare":     "Don't wait, {name}",
		"copy.rock":     "Rock'n'roll with {name}",
		"copy.escaped":  "It''s {name}''s turn",
		"cart.items":    "{count, plural, =0 {No items} one {# item} other {# items}}",
		"activity.who":  "{gender, select, female {She replied} other {Someone replied}}",
		"copy.reworded": "How's it going, {name}?",
	},
}

func verifyWorkspace(t *testing.T, edgeTexts map[string]map[string]string) (*workspace, *fakeEdge, []string) {
	t.Helper()
	format, runtime := jsModules(t)
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	old := fakeV03Texts(t, verifyV03)
	edge := newFakeEdge(t, "dk_test", "v0-check", "de", edgeTexts)
	w.env["GLOSSA_V0_KEY"] = "glossa_v03key"
	w.env["GLOSSA_DELIVERY_KEY"] = "dk_test"
	args := []string{"import", "--from", "v0", "--v0-url", old.URL, "--v0-project", "site", "--verify",
		"--edge", edge.srv.URL, "--environment", "v0-check", "--format-module", format, "--runtime-module", runtime}
	return w, edge, args
}

func cloneTexts(in map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for l, m := range in {
		out[l] = map[string]string{}
		for k, v := range m {
			out[l][k] = v
		}
	}
	return out
}

// `--verify` end to end, against v0.3's real formatter and the real
// runtime: the bare apostrophes are reported as v0.3's known defect,
// each with its key and evidence; a changed word beside an apostrophe
// stays a mismatch and fails the run; once the release says what v0.3
// says, the run passes with the known defects still listed.
func TestVerifyClassifiesEveryRendering(t *testing.T) {
	served := cloneTexts(verifyV03)
	served["de"]["copy.reworded"] = "Wie läuft's, {name}?" // a changed word, and an apostrophe
	w, edge, args := verifyWorkspace(t, served)

	var out verifyJSON
	w.json(&out, args...).want(t, ExitCheckFailed)
	if out.Schema != "glossa.cli.import-verify/v1" || out.Release == "" || out.Summary["keys"] != 6 || out.Summary["locales"] != 2 {
		t.Fatalf("report = %+v", out)
	}
	defects := map[string]int{}
	for _, r := range out.KnownDefects {
		defects[r.Key+" "+r.Locale]++
		if r.Defect != v0.DefectBareApostrophe || r.V0Requoted == nil || r.Runtime == nil || *r.V0Requoted != *r.Runtime {
			t.Errorf("known defect without its evidence: %+v", r)
		}
	}
	// copy.bare and copy.rock in both locales, and en's copy.reworded
	// ("How's it going"), which the release serves unchanged.
	for _, k := range []string{"copy.bare de", "copy.bare en", "copy.rock de", "copy.rock en", "copy.reworded en"} {
		if defects[k] != 1 {
			t.Errorf("%s: %d known-defect rows, want 1 (all: %v)", k, defects[k], defects)
		}
	}
	if len(out.Mismatches) != 1 || out.Mismatches[0].Key != "copy.reworded" || out.Mismatches[0].Locale != "de" {
		t.Fatalf("mismatches = %+v", out.Mismatches)
	}
	m := out.Mismatches[0]
	if m.Verdict != v0.VerdictMismatch || *m.V0 != "Wie gehts, {name}?" || *m.Runtime != "Wie läuft's, Ada?" {
		t.Errorf("the reworded row = %+v / %q / %q", m, *m.V0, *m.Runtime)
	}
	// cart.items: 0 1 2 5 21 in both locales (=0 is among them);
	// activity.who: female and the catch-all; the other four one set
	// each.
	if want := (5 + 2 + 4) * 2; out.Summary["renderings"] != want ||
		out.Summary["match"] != out.Summary["renderings"]-len(out.KnownDefects)-len(out.Mismatches) {
		t.Errorf("summary = %v", out.Summary)
	}

	human := w.run(args...)
	for _, want := range []string{"known v0.3 defect (v0_bare_apostrophe)", `~ copy.bare de {"name":"Ada"}: v0.3 "Gehts gut, {name}?", runtime "Geht's gut, Ada?"`,
		"1 renderings differ", `✗ copy.reworded de`} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stdout)
		}
	}

	// The release now says what v0.3 says: only the known defect is left,
	// and it is still reported.
	edge.publish(t, "v0-check", "de", verifyV03)
	w.json(&out, args...).want(t, ExitOK)
	if len(out.Mismatches) != 0 || out.Summary["known_defect"] != 6 || len(out.KnownDefects) != 6 {
		t.Errorf("after the fix: %d mismatches, summary %v", len(out.Mismatches), out.Summary)
	}

	// A key the release lacks is a mismatch, never a silent pass: the
	// runtime falls back, and the row says so.
	missing := cloneTexts(verifyV03)
	delete(missing["en"], "copy.escaped")
	edge.publish(t, "v0-check", "de", missing)
	w.json(&out, args...).want(t, ExitCheckFailed)
	if len(out.Mismatches) != 1 || !strings.Contains(out.Mismatches[0].RuntimeError, "resolved from") {
		t.Errorf("a key the release lacks: %+v", out.Mismatches)
	}
}

func TestVerifyUsage(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.env["GLOSSA_DELIVERY_KEY"] = "dk_test"
	base := []string{"import", "--from", "v0", "--v0-url", "http://x", "--v0-project", "site", "--verify"}
	var doc errorDoc
	for _, c := range []struct {
		args []string
		want string
	}{
		{append(base, "--environment", "e"), "--edge"},
		{append(base, "--edge", "http://edge"), "--environment"},
		{append(base, "--edge", "http://edge", "--environment", "e", "--dry-run"), "--verify only compares"},
		{append(base, "--edge", "http://edge", "--environment", "e", "--history"), "--verify only compares"},
		{[]string{"import", "--from", "v0", "--v0-url", "http://x", "--environment", "e"}, "--environment belongs to --verify"},
		{[]string{"import", "--format", "json", "f.json", "--verify"}, "--verify belongs to --from v0"},
	} {
		w.json(&doc, c.args...).want(t, ExitUsage)
		if !strings.Contains(doc.Error.Message+doc.Error.Fix, c.want) {
			t.Errorf("%v: error = %+v, want %q", c.args, doc.Error, c.want)
		}
	}
	// A module that is not where it is said to be is named.
	w.json(&doc, append(base, "--edge", "http://edge", "--environment", "e", "--format-module", filepath.Join(w.dir, "nope"))...).want(t, ExitUsage)
	if doc.Error.Code != "module_not_found" || !strings.Contains(doc.Error.Fix, "--format-module") {
		t.Errorf("a missing module: %+v", doc.Error)
	}
	w.env["GLOSSA_DELIVERY_KEY"] = ""
	w.json(&doc, append(base, "--edge", "http://edge", "--environment", "e")...).want(t, ExitUsage)
	if doc.Error.Code != "no_delivery_key" {
		t.Errorf("no delivery key: %+v", doc.Error)
	}
}
