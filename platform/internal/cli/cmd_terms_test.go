package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// termbase adds the cart concept: en "cart" preferred, de "Warenkorb"
// preferred and "Einkaufswagen" forbidden.
func termbase(t *testing.T, w *workspace) conceptJSON {
	t.Helper()
	var out termsChangeJSON
	w.json(&out, "terms", "add", "cart", "--preferred", "de=Warenkorb", "--forbidden", "de=Einkaufswagen",
		"--definition", "Where items wait before checkout", "--domain", "shop").want(t, ExitOK)
	return out.Concept
}

func TestTermsAddListShow(t *testing.T) {
	_, w := seeded(t)
	var added termsChangeJSON
	w.json(&added, "terms", "add", "cart", "--preferred", "de=Warenkorb", "--forbidden", "de=Einkaufswagen",
		"--definition", "Where items wait", "--part-of-speech", "noun").want(t, ExitOK)
	c := added.Concept
	if added.Schema != "glossa.cli.terms.change/v1" || added.Action != "created" || c.ProjectID == nil || *c.ProjectID != "prj_1" ||
		len(c.Terms) != 3 || c.Terms[0].Locale != "en" || c.Terms[0].Status != "preferred" || c.Terms[2].Status != "forbidden" {
		t.Fatalf("add = %+v", added)
	}
	var tenant termsChangeJSON
	w.json(&tenant, "terms", "add", "--preferred", "en=checkout", "--tenant-wide").want(t, ExitOK)
	if tenant.Concept.ProjectID != nil {
		t.Errorf("tenant-wide concept = %+v", tenant.Concept)
	}

	var list termsListJSON
	w.json(&list, "terms", "list").want(t, ExitOK)
	if list.Schema != "glossa.cli.terms.list/v1" || len(list.Concepts) != 2 {
		t.Fatalf("list = %+v", list)
	}
	w.json(&list, "terms", "list", "--query", "waren").want(t, ExitOK)
	if len(list.Concepts) != 1 || list.Concepts[0].ID != c.ID {
		t.Errorf("list --query = %+v", list)
	}
	h := w.run("terms", "list")
	if !strings.Contains(h.stdout, "cart (en) · Warenkorb (de) · Einkaufswagen (de, forbidden)") || !strings.Contains(h.stdout, "tenant") {
		t.Errorf("human list:\n%s", h.stdout)
	}

	var show termsShowJSON
	w.json(&show, "terms", "show", "Warenkorb").want(t, ExitOK)
	if show.Schema != "glossa.cli.terms.show/v1" || show.Concept.ID != c.ID {
		t.Fatalf("show by term = %+v", show)
	}
	w.json(&show, "terms", "show", c.ID).want(t, ExitOK)
	if show.Concept.Definition != "Where items wait" {
		t.Errorf("show by ID = %+v", show)
	}
	if h := w.run("terms", "show", c.ID); !strings.Contains(h.stdout, "Einkaufswagen") || !strings.Contains(h.stdout, "forbidden") {
		t.Errorf("human show:\n%s", h.stdout)
	}
	var e errorDoc
	w.json(&e, "terms", "show", "basket").want(t, ExitNetwork)
	if e.Error.Code != "term_not_found" || e.Error.Fix == "" {
		t.Errorf("unknown term = %+v", e)
	}
	w.json(&e, "terms", "add", "cart", "--preferred", "en=Cart").want(t, ExitUsage)
	if e.Error.Code != "duplicate_term" {
		t.Errorf("duplicate = %+v", e)
	}
	w.run("terms", "add").want(t, ExitUsage)
	w.run("terms", "add", "--preferred", "de").want(t, ExitUsage)
	w.run("terms", "add", "x", "--locale", "de", "--locale", "en").want(t, ExitUsage)
	w.run("terms").want(t, ExitUsage)
	w.run("terms", "import").want(t, ExitUsage)
}

func TestTermsEditDeprecateForbid(t *testing.T) {
	_, w := seeded(t)
	c := termbase(t, w)

	var out termsChangeJSON
	w.json(&out, "terms", "edit", c.ID, "--admitted", "de=Korb", "--remove", "de=Einkaufswagen", "--note", "UI term").want(t, ExitOK)
	if out.Action != "updated" || out.Concept.Version != c.Version+1 || out.Concept.Note != "UI term" || out.Concept.Definition != c.Definition ||
		termsSummary(out.Concept.Terms) != "cart (en) · Warenkorb (de) · Korb (de, admitted)" {
		t.Fatalf("edit = %+v", out)
	}
	w.json(&out, "terms", "edit", c.ID, "--note", "UI term").want(t, ExitOK)
	if out.Action != "unchanged" {
		t.Errorf("same edit = %+v", out)
	}

	w.json(&out, "terms", "deprecate", "Korb").want(t, ExitOK)
	if out.Action != "deprecated" || out.Concept.Terms[2].Status != "deprecated" {
		t.Fatalf("deprecate = %+v", out)
	}
	// With --concept, forbid adds a term the concept lacks.
	w.json(&out, "terms", "forbid", "basket", "--locale", "en", "--concept", c.ID).want(t, ExitOK)
	if out.Action != "forbidden" || termsSummary(out.Concept.Terms) != "cart (en) · Warenkorb (de) · Korb (de, deprecated) · basket (en, forbidden)" {
		t.Fatalf("forbid = %+v", out)
	}
	if h := w.run("terms", "forbid", "Warenkorb"); !strings.Contains(h.stdout, "forbidden") {
		t.Errorf("human forbid:\n%s", h.stdout)
	}

	var e errorDoc
	w.json(&e, "terms", "deprecate", "nothing").want(t, ExitNetwork)
	if e.Error.Code != "term_not_found" {
		t.Errorf("unknown term = %+v", e)
	}
	w.run("terms", "forbid", "basket", "--concept", c.ID).want(t, ExitOK) // already a term: its locale is known
	w.run("terms", "forbid", "trolley", "--concept", c.ID).want(t, ExitUsage)
	w.run("terms", "edit", c.ID, "--remove", "de=nope").want(t, ExitUsage)
	w.run("terms", "edit", "not-an-id").want(t, ExitUsage)
	w.run("terms", "forbid", "x", "--concept", "nope").want(t, ExitUsage)
}

func TestTermsCheckFindsForbiddenTerms(t *testing.T) {
	srv, w := seeded(t)
	termbase(t, w)
	w.write("locales/de.json", `{"cart.checkout": "Zum Einkaufswagen", "cart.items": "{count, plural, one {# Artikel} other {# Artikel}}", "checkout.pay": "Zahle {amount, number}"}`)
	w.run("push", "--translations").want(t, ExitOK)
	w.write("locales/en.json", `{"cart": {"checkout": "Your cart", "items": "{count, plural, one {# item} other {# items}}"}, "checkout.pay": "Pay {amount, number}"}`)
	w.run("push").want(t, ExitOK)

	var out termsCheckJSON
	w.json(&out, "terms", "check").want(t, ExitCheckFailed)
	if out.Schema != "glossa.cli.terms.check/v1" || out.Passed || out.Errors != 1 || out.Checked != 5 || len(out.Locales) != 2 {
		t.Fatalf("check = %+v", out)
	}
	var codes []string
	for _, f := range out.Findings {
		codes = append(codes, f.Locale+" "+f.Key+" "+f.Code+" "+f.Severity)
	}
	if strings.Join(codes, ",") != "de cart.checkout term_forbidden error,de cart.checkout term_missing warning" {
		t.Errorf("findings = %v", codes)
	}
	if f := out.Findings[0]; f.Text != "Einkaufswagen" || strings.Join(f.Suggestions, ",") != "Warenkorb" {
		t.Errorf("forbidden = %+v", f)
	}
	// The server checks every translation: one page for the project, no
	// request per translation, no snapshot of the project.
	srv.mu.Lock()
	pages, checks := srv.kn.termPages, srv.kn.termChecks
	srv.mu.Unlock()
	if pages != 1 || checks != 0 || srv.countRequests("GET /v1/tenants/ten_1/projects/prj_1/translations") != 0 {
		t.Errorf("terms check sent %d pages, %d single checks", pages, checks)
	}

	h := w.run("terms", "check")
	h.want(t, ExitCheckFailed)
	for _, want := range []string{"✓ 5 translations checked against the termbase", "✗ de 1 error, 1 warning",
		`error cart.checkout  term_forbidden: "Einkaufswagen" is forbidden (use "Warenkorb")`, "✓ ja no findings", "Terminology check failed"} {
		if !strings.Contains(h.stdout, want) {
			t.Errorf("human check lacks %q:\n%s", want, h.stdout)
		}
	}

	// Only ja: nothing to find; --states narrows what is checked.
	w.json(&out, "terms", "check", "--locale", "ja").want(t, ExitOK)
	if !out.Passed || len(out.Locales) != 1 || out.Checked != 2 {
		t.Errorf("ja only = %+v", out)
	}
	w.json(&out, "terms", "check", "--states", "approved").want(t, ExitOK)
	if out.Checked != 0 {
		t.Errorf("approved only = %+v", out)
	}
	w.run("terms", "check", "--locale", "fr").want(t, ExitUsage)
	w.run("terms", "check", "--states", "done").want(t, ExitUsage)

	// glossa check --terminology adds the layer to structural QA.
	var chk checkJSON
	w.json(&chk, "check", "--terminology", "--require-complete=none").want(t, ExitCheckFailed)
	var forbidden domain.Finding
	for _, f := range chk.Findings {
		if f.Layer == domain.LayerTerminology && f.Code == "term_forbidden" &&
			f.Locus.Locale == "de" && f.Locus.Key == "cart.checkout" {
			forbidden = f
		}
	}
	if forbidden.Code == "" || chk.Errors != 1 {
		t.Errorf("check --terminology = %+v", chk)
	}
	// The termbase answers by key; the identity a waiver is written
	// against is the catalog message ID, and the pull request's own
	// terminology findings carry it. The terminal must print the same
	// fingerprint (RFC 0005 §2.1).
	if forbidden.Locus.Message != "msg_cart.checkout" {
		t.Errorf("locus = %+v, want the catalog message ID the snapshot resolved", forbidden.Locus)
	}
	if want := domain.Fingerprint(domain.LayerTerminology, "term_forbidden",
		domain.Locus{Message: "msg_cart.checkout", Locale: "de"}, forbidden.Subject); forbidden.Fingerprint != want {
		t.Errorf("fingerprint = %s, want the print over the message ID (%s)", forbidden.Fingerprint, want)
	}
	srv.mu.Lock()
	before := srv.kn.termPages
	srv.mu.Unlock()
	w.json(&chk, "check", "--require-complete=none").want(t, ExitOK)
	srv.mu.Lock()
	after := srv.kn.termPages
	srv.mu.Unlock()
	if after != before {
		t.Error("plain check asked the termbase")
	}
	w.run("check", "--terminology", "--offline").want(t, ExitUsage)
}

// A terminology finding carries its message's namespace, and a policy
// rule that selects on one can therefore select it.
//
// This is the whole point of filling it. `{layer: terminology,
// namespace: legal, severity: …}` matched nothing while every
// terminology finding's `locus.namespace` was empty, so a project could
// write the rule, read it back from `glossa policy show`, and have it do
// nothing at all. A selector that silently matches nothing is worse than
// one that errors.
func TestTerminologyFindingsCarryTheirNamespace(t *testing.T) {
	check := func(t *testing.T, namespace string, rules ...map[string]any) checkJSON {
		t.Helper()
		srv, w := seeded(t)
		termbase(t, w)
		w.write("locales/de.json", `{"cart.checkout": "Zum Einkaufswagen"}`)
		w.run("push", "--translations").want(t, ExitOK)
		doc := map[string]any{"schema": "glossa.check-policy/v1", "require_complete": "none",
			"fail_on": "error", "missing_translations": "error"}
		if len(rules) > 0 {
			doc["rules"] = rules
		}
		srv.mu.Lock()
		srv.messages["cart.checkout"].namespace = namespace
		srv.policyDoc = map[string]any{"version": 3, "document": doc}
		srv.mu.Unlock()
		var out checkJSON
		w.json(&out, "check", "--terminology")
		return out
	}
	forbidden := func(t *testing.T, doc checkJSON) domain.Finding {
		t.Helper()
		for _, f := range doc.Findings {
			if f.Layer == domain.LayerTerminology && f.Code == "term_forbidden" {
				return f
			}
		}
		t.Fatalf("no terminology finding in %+v", doc.Findings)
		return domain.Finding{}
	}

	// Without a rule: the finding is an error because that is the
	// code's own severity, and it names the namespace it is in.
	plain := forbidden(t, check(t, "legal"))
	if plain.Locus.Namespace != "legal" {
		t.Errorf("locus = %+v, want the message's namespace", plain.Locus)
	}
	if plain.Severity != domain.Error {
		t.Errorf("severity = %s, want the code's own", plain.Severity)
	}

	// With the rule: the selector matches, and the severity it states
	// is the one the finding gets. Nothing but the namespace can have
	// decided that.
	ruled := forbidden(t, check(t, "legal",
		map[string]any{"layer": "terminology", "namespace": "legal", "severity": "warning"}))
	if ruled.Severity != domain.Warning {
		t.Errorf("severity = %s, want the rule's — the namespace selector still matches nothing", ruled.Severity)
	}
	if ruled.Fingerprint != plain.Fingerprint {
		t.Errorf("the severity moved the print: %s vs %s", ruled.Fingerprint, plain.Fingerprint)
	}

	// And it is really the namespace that decides: the same rule over
	// another namespace leaves the finding alone.
	other := forbidden(t, check(t, "marketing",
		map[string]any{"layer": "terminology", "namespace": "legal", "severity": "warning"}))
	if other.Severity != domain.Error || other.Locus.Namespace != "marketing" {
		t.Errorf("a legal rule graded a marketing finding: %+v", other)
	}
	// The namespace is context, not identity: the three runs are three
	// namespaces and one print (RFC 0005 §2.1).
	if other.Fingerprint != plain.Fingerprint {
		t.Errorf("the namespace moved the print: %s vs %s", other.Fingerprint, plain.Fingerprint)
	}
}

// A waived terminology finding comes back when the source under it
// moves (RFC 0005 §2.3, §12.4).
//
// It never did: the terminology layer's findings carried no source
// revision, and domain.Waiver.Stale is false for a finding that names
// none — nothing to disagree with — so a waiver on one never expired,
// whatever happened to the source. The server names the revision it
// checked against, and both `glossa terms check` and `glossa check
// --terminology` now carry it.
func TestAWaivedTerminologyFindingComesBackWhenItsSourceMoves(t *testing.T) {
	_, w := seeded(t)
	termbase(t, w)
	w.write("locales/de.json", `{"cart.checkout": "Zum Einkaufswagen"}`)
	w.run("push", "--translations").want(t, ExitOK)

	forbidden := func(t *testing.T) domain.Finding {
		t.Helper()
		var out checkJSON
		w.json(&out, "check", "--terminology")
		for _, f := range out.Findings {
			if f.Layer == domain.LayerTerminology && f.Code == "term_forbidden" {
				return f
			}
		}
		t.Fatalf("no term_forbidden in %+v", out.Findings)
		return domain.Finding{}
	}

	before := forbidden(t)
	if before.SourceRevision == nil || *before.SourceRevision != 1 {
		t.Fatalf("source_revision = %v, want the message's revision 1", before.SourceRevision)
	}
	var terms termsCheckJSON
	w.json(&terms, "terms", "check").want(t, ExitCheckFailed)
	if len(terms.Findings) == 0 || terms.Findings[0].SourceRevision != 1 {
		t.Errorf("terms check = %+v, want each finding at source revision 1", terms.Findings)
	}

	w.run("waive", before.Fingerprint, "--reason", "the shop says Einkaufswagen",
		"--source-revision", "1").want(t, ExitOK)
	if got := forbidden(t); got.Severity != domain.Waived {
		t.Fatalf("after the waiver: %s, want waived", got.Severity)
	}

	// The English under it changes, and the German does not: the waiver
	// was made against a source that no longer ships.
	w.write("locales/en.json", `{"cart": {"checkout": "Go to checkout", "items": "{count, plural, one {# item} other {# items}}"}, "checkout.pay": "Pay {amount, number}"}`)
	w.run("push").want(t, ExitOK)
	after := forbidden(t)
	switch {
	case after.Fingerprint != before.Fingerprint:
		t.Fatalf("a different finding: %s, want %s — the revision is context, never identity",
			after.Fingerprint, before.Fingerprint)
	case after.SourceRevision == nil || *after.SourceRevision != 2:
		t.Fatalf("source_revision = %v, want 2", after.SourceRevision)
	case after.Severity != domain.Error:
		t.Errorf("severity = %s, want the finding back at error: its waiver went stale", after.Severity)
	}
}
