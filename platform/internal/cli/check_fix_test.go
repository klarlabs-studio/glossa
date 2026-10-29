package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// fixWorkspace is a project directory with a German catalog and nothing
// else, for the cases that are about applyFixes rather than about a
// whole check run.
func fixWorkspace(t *testing.T, de map[string]string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	body, err := json.Marshal(de)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locales", "de.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		Path: filepath.Join(dir, "glossa.yaml"), SourceLocale: "en",
		Catalogs: config.Catalogs{Path: "locales/{locale}.json"},
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// `glossa check --fix` (RFC 0005 §13 wave 6).
//
// The discipline under test is one sentence: apply only what a fix
// actually describes. Every test here is a case of that — the ones that
// name text are applied, and the ones that name an intention are
// reported with why.

// fixesByKey indexes a check document's fixes.
func fixesByKey(out checkJSON) map[string]fixJSON {
	byKey := map[string]fixJSON{}
	for _, f := range out.Fixes {
		byKey[f.Locale+" "+f.Key] = f
	}
	return byKey
}

// termFixable is a project whose German catalog uses a forbidden term,
// so the terminology layer proposes a use-term fix for it.
func termFixable(t *testing.T) *workspace {
	t.Helper()
	_, w := seeded(t)
	termbase(t, w)
	w.write("locales/de.json", `{"cart.checkout": "Zum Einkaufswagen", "cart.items": "{count, plural, one {# Artikel} other {# Artikel}}", "checkout.pay": "Zahle {amount, number}"}`)
	w.run("push", "--translations").want(t, ExitOK)
	return w
}

// A `use-term` fix names a term *and*, through the span, the bytes it
// replaces. That is an edit, and it is made — in place, leaving the
// rest of the message alone.
func TestCheckFixAppliesAUseTermFixInsideTheMessage(t *testing.T) {
	w := termFixable(t)

	var out checkJSON
	w.json(&out, "check", "--terminology", "--fix").want(t, ExitCheckFailed)

	fix, ok := fixesByKey(out)["de cart.checkout"]
	if !ok {
		t.Fatalf("no fix for the forbidden term: %+v", out.Fixes)
	}
	if !fix.Applied || fix.Kind != domain.FixUseTerm {
		t.Fatalf("fix = %+v", fix)
	}
	if fix.From != "Zum Einkaufswagen" || fix.To != "Zum Warenkorb" {
		t.Fatalf("fix rewrote %q to %q, want only the term replaced", fix.From, fix.To)
	}
	if fix.File != "locales/de.json" {
		t.Errorf("file = %q", fix.File)
	}
	// And the catalog on disk says it.
	if got := w.read("locales/de.json"); !strings.Contains(got, `"Zum Warenkorb"`) {
		t.Fatalf("locales/de.json:\n%s", got)
	}
	// The other messages are untouched.
	if got := w.read("locales/de.json"); !strings.Contains(got, `"Zahle {amount, number}"`) {
		t.Errorf("another message changed:\n%s", got)
	}
	// The verdict is the run's own, from before the edits: the check
	// graded what it found, and a green exit would be grading a project
	// nobody has checked.
	if out.Passed {
		t.Error("the run reports passing after --fix, but it is the run before the fixes")
	}
}

// The verdict a --fix run prints is the run's own, so the output has to
// say what to do about that.
func TestCheckFixSaysToRunTheCheckAgain(t *testing.T) {
	w := termFixable(t)

	h := w.run("check", "--terminology", "--fix")
	h.want(t, ExitCheckFailed)
	if !strings.Contains(h.stdout, "fixed de cart.checkout") {
		t.Errorf("the output doesn't name what it fixed:\n%s", h.stdout)
	}
	if !strings.Contains(h.stdout, "run `glossa check` again to confirm") {
		t.Errorf("the output doesn't say to run again:\n%s", h.stdout)
	}
}

// Running it again is not a second edit. The catalog has moved on and
// the finding's span has not, so the guard stops it: the rule is the
// same one, applied to the run's own leftovers.
func TestCheckFixDoesNotApplyTheSameFixTwice(t *testing.T) {
	w := termFixable(t)
	w.run("check", "--terminology", "--fix").want(t, ExitCheckFailed)
	after := w.read("locales/de.json")

	// The server still holds the old translation, so the finding is made
	// again — against a catalog that no longer reads what it found.
	var again checkJSON
	w.json(&again, "check", "--terminology", "--fix").want(t, ExitCheckFailed)
	fix := fixesByKey(again)["de cart.checkout"]
	if fix.Applied {
		t.Fatalf("the fix was applied twice: %+v", fix)
	}
	if !strings.Contains(fix.Why, "changed since") {
		t.Errorf("why = %q", fix.Why)
	}
	if w.read("locales/de.json") != after {
		t.Error("the catalog changed on the second run")
	}
}

// A fix that names an intention rather than text is reported and not
// applied. `adopt-source-change` asks for a translation, and no amount
// of string handling makes one.
func TestCheckFixNeverGuessesAnEdit(t *testing.T) {
	srv, w := seeded(t)
	// An outdated translation: the completeness layer proposes
	// adopt-source-change for it.
	w.write("locales/en.json", `{"cart": {"checkout": "Proceed to checkout", "items": "{count, plural, one {# item} other {# items}}"}, "checkout.pay": "Pay {amount, number}"}`)
	w.run("push").want(t, ExitOK)
	_ = srv

	var out checkJSON
	w.json(&out, "check", "--fix").want(t, ExitCheckFailed)
	var adopt *fixJSON
	for i, f := range out.Fixes {
		if f.Kind == domain.FixAdoptSourceChange {
			adopt = &out.Fixes[i]
			break
		}
	}
	if adopt == nil {
		t.Fatalf("no adopt-source-change fix among %+v", out.Fixes)
	}
	if adopt.Applied {
		t.Fatalf("an adopt-source-change fix was applied: %+v", adopt)
	}
	if !strings.Contains(adopt.Why, "translation") {
		t.Errorf("why = %q, want it to say a translation is not an edit", adopt.Why)
	}
	// The catalog is as it was.
	if got := w.read("locales/de.json"); !strings.Contains(got, "Zur Kasse") {
		t.Errorf("the catalog changed:\n%s", got)
	}
}

// The span guard: a span is an offset into the text the layer saw, and
// text that has moved on since makes the same offsets point at
// different words. Writing there would be a guess dressed as a fix.
func TestFixedTextRefusesASpanTheCatalogNoLongerMatches(t *testing.T) {
	finding := domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: domain.Error,
		Locus: domain.Locus{Key: "cart.checkout", Locale: "de",
			Span: &domain.Span{Side: domain.SideTarget, Start: 4, End: 17}},
		Subject: "Einkaufswagen", Fix: &domain.Fix{Kind: domain.FixUseTerm, Hint: "Warenkorb"},
	})
	if got, why := fixedText("Zum Einkaufswagen", finding); why != "" || got != "Zum Warenkorb" {
		t.Fatalf("on the text the layer saw: %q, %q", got, why)
	}
	for _, tc := range []struct{ name, text, want string }{
		{"the words changed", "Zum Warenkorb jetzt", "the catalog reads"},
		{"the text is shorter now", "Zum", "outside the catalog's text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := fixedText(tc.text, finding)
			if why == "" {
				t.Fatalf("applied anyway: %q", got)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("why = %q, want it to mention %q", why, tc.want)
			}
		})
	}
}

// A `shorten` that names a length and no words is a target, not an
// edit. A `replace` or `shorten` that names the text is applied whole.
func TestFixedTextAppliesOnlyWhatNamesText(t *testing.T) {
	to := 28
	base := domain.Finding{
		Layer: domain.LayerLength, Code: "expansion-excessive", Severity: domain.Warning,
		Locus: domain.Locus{Key: "checkout.pay", Locale: "fr"},
	}
	withFix := func(f domain.Fix) domain.Finding {
		out := base
		out.Fix = &f
		return out
	}
	if got, why := fixedText("Payer le montant maintenant",
		withFix(domain.Fix{Kind: domain.FixShorten, To: &to, Hint: "Payer maintenant"})); why != "" || got != "Payer maintenant" {
		t.Errorf("shorten with a hint = %q, %q", got, why)
	}
	if got, why := fixedText("Payer", withFix(domain.Fix{Kind: domain.FixReplace, Hint: "Régler"})); why != "" || got != "Régler" {
		t.Errorf("replace = %q, %q", got, why)
	}
	_, why := fixedText("Payer le montant maintenant", withFix(domain.Fix{Kind: domain.FixShorten, To: &to}))
	if !strings.Contains(why, "28 characters") {
		t.Errorf("shorten with no hint: why = %q", why)
	}
	_, why = fixedText("Payer", withFix(domain.Fix{Kind: domain.FixUseTerm, Hint: "Régler"}))
	if !strings.Contains(why, "which words") {
		t.Errorf("use-term with no span: why = %q", why)
	}
	_, why = fixedText("Payer", withFix(domain.Fix{Kind: "invent-something"}))
	if !strings.Contains(why, "not a fix this command knows") {
		t.Errorf("an unknown kind: why = %q", why)
	}
}

// A waived finding is one somebody accepted on purpose. Fixing it would
// undo that decision on their behalf.
func TestCheckFixLeavesWaivedFindingsAlone(t *testing.T) {
	cfg := fixWorkspace(t, map[string]string{"cart.checkout": "Zum Einkaufswagen"})
	inv := &invocation{env: Env{Dir: cfg.Dir()}}
	waived := domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_forbidden", Locus: domain.Locus{Key: "cart.checkout", Locale: "de",
			Span: &domain.Span{Side: domain.SideTarget, Start: 4, End: 17}},
		Subject: "Einkaufswagen", Fix: &domain.Fix{Kind: domain.FixUseTerm, Hint: "Warenkorb"},
	}).Waive("wv_1")

	fixes := inv.applyFixes(cfg, []domain.Finding{waived})
	if len(fixes) != 1 || fixes[0].Applied {
		t.Fatalf("fixes = %+v", fixes)
	}
	if !strings.Contains(fixes[0].Why, "waived") {
		t.Errorf("why = %q", fixes[0].Why)
	}
}

// A second fix's span was measured against the text the first one
// replaced, so a run applies at most one fix per message and says to
// run again. Two passes that are each correct beat one that is probably
// correct.
func TestCheckFixAppliesOneFixPerMessagePerRun(t *testing.T) {
	cfg := fixWorkspace(t, map[string]string{"cart.checkout": "Zum Einkaufswagen"})
	inv := &invocation{env: Env{Dir: cfg.Dir()}}
	first := domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_forbidden", Locus: domain.Locus{Key: "cart.checkout", Locale: "de",
			Span: &domain.Span{Side: domain.SideTarget, Start: 4, End: 17}},
		Subject: "Einkaufswagen", Fix: &domain.Fix{Kind: domain.FixUseTerm, Hint: "Warenkorb"},
	})
	second := domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_deprecated", Locus: domain.Locus{Key: "cart.checkout", Locale: "de",
			Span: &domain.Span{Side: domain.SideTarget, Start: 0, End: 3}},
		Subject: "Zum", Fix: &domain.Fix{Kind: domain.FixUseTerm, Hint: "Zur"},
	})

	fixes := inv.applyFixes(cfg, []domain.Finding{first, second})
	if len(fixes) != 2 || !fixes[0].Applied {
		t.Fatalf("fixes = %+v", fixes)
	}
	if fixes[1].Applied || !strings.Contains(fixes[1].Why, "run `glossa check --fix` again") {
		t.Fatalf("the second fix = %+v", fixes[1])
	}
}

// A locale with no catalog on disk, and a key the catalog doesn't hold,
// are reported rather than invented: --fix edits messages, it does not
// create them.
func TestCheckFixReportsWhatItCantReach(t *testing.T) {
	cfg := fixWorkspace(t, map[string]string{"cart.checkout": "Zum Einkaufswagen"})
	inv := &invocation{env: Env{Dir: cfg.Dir()}}
	unknownKey := domain.New(domain.Finding{
		Layer: domain.LayerLength, Code: "expansion-excessive", Locus: domain.Locus{Key: "checkout.pay", Locale: "de"},
		Fix: &domain.Fix{Kind: domain.FixReplace, Hint: "Zahlen"},
	})
	unknownLocale := domain.New(domain.Finding{
		Layer: domain.LayerLength, Code: "expansion-excessive", Locus: domain.Locus{Key: "cart.checkout", Locale: "ja"},
		Fix: &domain.Fix{Kind: domain.FixReplace, Hint: "レジへ"},
	})
	noLocus := domain.New(domain.Finding{
		Layer: domain.LayerCompleteness, Code: "missing-locale",
		Fix: &domain.Fix{Kind: domain.FixReplace, Hint: "anything"},
	})

	fixes := inv.applyFixes(cfg, []domain.Finding{unknownKey, unknownLocale, noLocus})
	if len(fixes) != 3 {
		t.Fatalf("fixes = %+v", fixes)
	}
	for i, want := range []string{"has no checkout.pay", "no local catalog at", "names no message and locale"} {
		if fixes[i].Applied || !strings.Contains(fixes[i].Why, want) {
			t.Errorf("fix %d = %+v, want %q", i, fixes[i], want)
		}
	}
	// Nothing was written.
	if got := readFile(t, cfg.CatalogPath("de")); !strings.Contains(got, "Zum Einkaufswagen") {
		t.Errorf("the catalog changed:\n%s", got)
	}
}

// A fix whose text the catalog already holds is not an edit.
func TestCheckFixSkipsWhatTheCatalogAlreadySays(t *testing.T) {
	cfg := fixWorkspace(t, map[string]string{"cart.checkout": "Zur Kasse"})
	inv := &invocation{env: Env{Dir: cfg.Dir()}}
	same := domain.New(domain.Finding{
		Layer: domain.LayerLength, Code: "expansion-excessive", Locus: domain.Locus{Key: "cart.checkout", Locale: "de"},
		Fix: &domain.Fix{Kind: domain.FixReplace, Hint: "Zur Kasse"},
	})

	fixes := inv.applyFixes(cfg, []domain.Finding{same})
	if len(fixes) != 1 || fixes[0].Applied {
		t.Fatalf("fixes = %+v", fixes)
	}
	if !strings.Contains(fixes[0].Why, "already says this") {
		t.Errorf("why = %q", fixes[0].Why)
	}
}

// Without --fix nothing is applied and nothing is reported: a fix is a
// hint until someone asks (RFC 0005 §2.1).
func TestCheckWithoutFixChangesNothing(t *testing.T) {
	w := termFixable(t)
	before := w.read("locales/de.json")

	var out checkJSON
	w.json(&out, "check", "--terminology").want(t, ExitCheckFailed)
	if out.Fixes != nil {
		t.Errorf("fixes = %+v, want none without --fix", out.Fixes)
	}
	if w.read("locales/de.json") != before {
		t.Error("the catalog changed without --fix")
	}
}
