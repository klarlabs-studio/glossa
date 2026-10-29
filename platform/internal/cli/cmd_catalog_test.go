package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
)

const sourceEN = `{
  "cart": {"checkout": "Checkout", "items": "{count, plural, one {# item} other {# items}}"},
  "checkout.pay": "Pay {amount, number}"
}`

func TestPushCreatesThenLeavesUnchanged(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})

	var dry pushJSON
	w.json(&dry, "push", "--dry-run").want(t, ExitOK)
	if !dry.DryRun || dry.Summary["created"] != 3 || srv.countRequests("POST") != 0 {
		t.Fatalf("dry run = %+v (writes: %d)", dry, srv.countRequests("POST"))
	}

	var out pushJSON
	w.json(&out, "push").want(t, ExitOK)
	if out.Summary["created"] != 3 || out.Source != "locales/en.json" || out.Messages[0].Key != "cart.checkout" || out.Messages[0].Revision != 1 {
		t.Fatalf("push = %+v", out)
	}
	w.json(&out, "push").want(t, ExitOK)
	if out.Summary["unchanged"] != 3 {
		t.Errorf("second push = %+v", out.Summary)
	}

	w.write("locales/en.json", strings.Replace(sourceEN, "Checkout", "Go to checkout", 1))
	w.json(&dry, "push", "--dry-run").want(t, ExitOK)
	if dry.Summary["revised"] != 1 || dry.Summary["unchanged"] != 2 {
		t.Errorf("dry run after an edit = %+v", dry.Summary)
	}
	r := w.run("push")
	r.want(t, ExitOK)
	if !strings.Contains(r.stdout, "1 revised") {
		t.Errorf("human push = %s", r.stdout)
	}
}

func TestPushReportsInvalidMessagesAsPartialFailure(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, map[string]string{"en": `{"ok": "Fine", "broken": "{count, plural, one {x}", "Bad Key": "x"}`})
	var out pushJSON
	w.json(&out, "push").want(t, ExitPartial)
	codes := map[string]string{}
	for _, it := range out.Messages {
		if it.Error != nil {
			codes[it.Key] = it.Error.Code
		}
	}
	if out.Summary["created"] != 1 || codes["broken"] != "invalid_message" || codes["Bad Key"] != "invalid_message_key" {
		t.Errorf("push = %+v", out)
	}
}

// Issue #69: on an empty project the dry run treats the sources it would
// create as existing for the translation step.
func TestPushTranslationsDryRunCountsSourcesItWouldCreate(t *testing.T) {
	srv := newFakeServer(t)
	srv.locales = []string{"de", "en"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN,
		"de": `{"cart.checkout": "Zur Kasse"}`})
	var dry pushJSON
	w.json(&dry, "push", "--translations", "--dry-run").want(t, ExitOK)
	if srv.countRequests("POST") != 0 || dry.Summary["failed"] != 0 || len(dry.Translations) != 1 {
		t.Fatalf("dry run = %+v (writes: %d)", dry, srv.countRequests("POST"))
	}
	if it := dry.Translations[0]; it.Status != "created" || it.Note != "would be created with the source push" {
		t.Errorf("translation = %+v", it)
	}
	// The real run creates it, as the dry run said.
	var out pushJSON
	w.json(&out, "push", "--translations").want(t, ExitOK)
	if out.Translations[0].Status != "created" {
		t.Errorf("real run = %+v", out.Translations)
	}
}

func TestPushTranslationsImportsLocalCatalogs(t *testing.T) {
	srv := newFakeServer(t)
	srv.locales = []string{"de", "en"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN,
		"de": `{"cart.checkout": "Zur Kasse", "checkout.pay": "Bezahlen"}`})
	var out pushJSON
	w.json(&out, "push", "--translations").want(t, ExitPartial)
	byKey := map[string]pushItem{}
	for _, it := range out.Translations {
		byKey[it.Key] = it
	}
	if byKey["cart.checkout"].Status != "created" || byKey["cart.checkout"].State != "needs_review" {
		t.Errorf("cart.checkout = %+v", byKey["cart.checkout"])
	}
	if e := byKey["checkout.pay"].Error; e == nil || e.Code != "structural_qa_failed" {
		t.Errorf("checkout.pay (drops {amount}) = %+v", byKey["checkout.pay"])
	}
}

func TestSourceLocaleMismatchIsAConfigError(t *testing.T) {
	srv := newFakeServer(t)
	srv.sourceLocale = "de"
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	var doc errorDoc
	w.json(&doc, "push").want(t, ExitUsage)
	if doc.Error.Code != "source_locale_mismatch" {
		t.Errorf("error = %+v", doc.Error)
	}
}

// seeded is a project with en source, de complete and ja missing one.
func seeded(t *testing.T) (*fakeServer, *workspace) {
	t.Helper()
	srv := newFakeServer(t)
	srv.locales = []string{"de", "en", "ja"}
	w := newWorkspace(t).withProject(srv, map[string]string{"en": sourceEN})
	w.run("push").want(t, ExitOK)
	w.write("locales/de.json", `{"cart.checkout": "Zur Kasse", "cart.items": "{count, plural, one {# Artikel} other {# Artikel}}", "checkout.pay": "Zahle {amount, number}"}`)
	w.write("locales/ja.json", `{"cart.checkout": "レジへ", "cart.items": "{count, plural, other {#個}}"}`)
	w.run("push", "--translations").want(t, ExitOK)
	return srv, w
}

func TestCheckFailsOnMissingTranslationsAndHonorsPolicy(t *testing.T) {
	_, w := seeded(t)
	var out checkJSON
	w.json(&out, "check").want(t, ExitCheckFailed)
	if out.Passed || out.Schema != "glossa.cli.check/v2" || out.Messages != 3 || out.Policy.RequireComplete != nil {
		t.Fatalf("check = %+v", out)
	}
	var missing []string
	for _, f := range out.Findings {
		if f.Code == qa.CodeMissingTranslation {
			missing = append(missing, f.Locus.Locale+" "+f.Locus.Key+" "+string(f.Severity))
		}
	}
	if strings.Join(missing, ",") != "ja checkout.pay error" {
		t.Errorf("missing = %v", missing)
	}

	var deOnly checkJSON
	w.json(&deOnly, "check", "--require-complete=de").want(t, ExitOK)
	// Four warnings: the Japanese key nobody translated, and the three
	// the source layer has about the fixture's own copy — "Checkout" is
	// one word with no description, and neither interpolating message
	// says what its placeholder holds. They are warnings and the run
	// passes, which is what RFC 0005 §3.5 asks for: source copy is the
	// product team's call.
	if !deOnly.Passed || deOnly.Warnings != 4 {
		t.Errorf("de required = %+v", deOnly)
	}
	w.json(&checkJSON{}, "check", "--require-complete=de", "--fail-on=warning").want(t, ExitCheckFailed)
	w.json(&checkJSON{}, "check", "--require-complete=none").want(t, ExitOK)
	w.json(&checkJSON{}, "check", "--require-complete=de,fr").want(t, ExitCheckFailed)
	w.run("check", "--fail-on=info").want(t, ExitUsage)
	w.run("check", "--require-complete=nöt").want(t, ExitUsage)
}

func TestCheckHumanOutputReadsLikeCI(t *testing.T) {
	_, w := seeded(t)
	r := w.run("check")
	r.want(t, ExitCheckFailed)
	for _, want := range []string{"✓ 3 messages discovered", "✓ message structures valid", "✓ parity valid",
		"✓ de complete", "✗ ja 1 missing", "checkout.pay  missing translation",
		// The three source-layer warnings about the fixture's own copy
		// are reported beside the error and do not change the verdict.
		"ambiguous-short", "missing-description",
		"Localization check failed: 1 error, 3 warnings."} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := w.run("check", "--quiet"); r.stdout != "" || r.code != int(ExitCheckFailed) {
		t.Errorf("--quiet printed %q (exit %d)", r.stdout, r.code)
	}
}

func TestCheckOfflineUsesLocalCatalogsAndTheKernel(t *testing.T) {
	w := newWorkspace(t).withProject(nil, map[string]string{
		"en": sourceEN,
		"de": `{"cart.checkout": "Zur Kasse", "cart.items": "{n, plural, one {# Artikel} other {# Artikel}}", "checkout.pay": "Zahle {amount, number}", "old.key": "Alt"}`,
	})
	var out checkJSON
	w.json(&out, "check", "--offline").want(t, ExitCheckFailed)
	got := map[string]string{}
	for _, f := range out.Findings {
		got[f.Locus.Key+" "+f.Code] = string(f.Severity)
	}
	if got["cart.items missing-argument"] != "error" || got["cart.items extra-argument"] != "error" || got["old.key unknown-key"] != "warning" {
		t.Errorf("findings = %v", got)
	}
	if out.Origin != "local" || out.Findings[0].Locus.File == "" {
		t.Errorf("report = %+v", out)
	}
}

func TestStatusCountsCoverage(t *testing.T) {
	srv, w := seeded(t)
	srv.translations["de"]["cart.checkout"].state = "approved"
	var out statusJSON
	w.json(&out, "status").want(t, ExitOK)
	if len(out.Locales) != 3 || out.Locales[0].Code != "en" || !out.Locales[0].IsSource {
		t.Fatalf("status = %+v", out)
	}
	de, ja := out.Locales[1], out.Locales[2]
	if de.Translated != 3 || de.Approved != 1 || de.NeedsReview != 2 || de.Coverage != 1 {
		t.Errorf("de = %+v", de)
	}
	if ja.Missing != 1 || ja.Translated != 2 {
		t.Errorf("ja = %+v", ja)
	}
	r := w.run("status")
	if !strings.Contains(r.stdout, "COVERAGE") || !strings.Contains(r.stdout, "66.7%") {
		t.Errorf("status table = %s", r.stdout)
	}
}

// check, diff and pull read translations with the bulk listing (paged),
// status reads the stats; none reads one message at a time.
func TestReadsTranslationsInBulk(t *testing.T) {
	srv, w := seeded(t)
	const p = "GET /v1/tenants/ten_1/projects/prj_1"
	before := srv.countRequests(p + "/translations ")
	w.json(&statusJSON{}, "status").want(t, ExitOK)
	if srv.countRequests(p+"/translation-stats ") != 1 || srv.countRequests(p+"/translations ") != before {
		t.Errorf("status: %d stats reads, %d listings", srv.countRequests(p+"/translation-stats "), srv.countRequests(p+"/translations ")-before)
	}
	w.json(&checkJSON{}, "check").want(t, ExitCheckFailed)
	w.json(&diffJSON{}, "diff").want(t, ExitOK)
	w.json(&pullJSON{}, "pull").want(t, ExitOK)
	// 5 translations across de and ja, 2 a page: 3 pages per command,
	// and check's one bounded page of the translations of obsolete
	// messages (layers.MaxOrphans), which it never pages past.
	if n := srv.countRequests(p+"/translations ") - before; n != 10 {
		t.Errorf("%d listing requests for check, diff and pull, want 10", n)
	}
	if n := srv.countRequests(p + "/messages/"); n != 0 {
		t.Errorf("%d per-message reads", n)
	}
}

func TestDiffComparesCanonicalModels(t *testing.T) {
	_, w := seeded(t)
	var out diffJSON
	w.json(&out, "diff", "--exit-code").want(t, ExitOK)
	if !out.Identical {
		t.Fatalf("diff after push = %+v", out)
	}
	// Same message, different spelling: not a change.
	w.write("locales/en.json", `{"cart.checkout": "Checkout", "cart.items": "{count,plural,one{# item}other{# items}}", "checkout.pay": "Pay {amount, number}", "new.one": "New"}`)
	w.write("locales/de.json", `{"cart.checkout": "Kasse"}`)
	w.json(&out, "diff", "--exit-code").want(t, ExitCheckFailed)
	if strings.Join(out.Source.Added, ",") != "new.one" || len(out.Source.Changed) != 0 || out.Source.Unchanged != 3 {
		t.Errorf("source diff = %+v", out.Source)
	}
	var de diffSetJSON
	for _, d := range out.Translations {
		if d.Locale == "de" {
			de = d
		}
	}
	if len(de.Changed) != 1 || de.Changed[0].Server != "Zur Kasse" || len(de.Removed) != 2 {
		t.Errorf("de diff = %+v", de)
	}
}

func TestLocalesAndMessagesList(t *testing.T) {
	_, w := seeded(t)
	var ls localesJSON
	w.json(&ls, "locales").want(t, ExitOK)
	if len(ls.Locales) != 3 || ls.Locales[0].Code != "en" || ls.Fallback["*"][0] != "en" {
		t.Errorf("locales = %+v", ls)
	}
	var ms messagesJSON
	w.json(&ms, "messages", "--missing-in", "ja").want(t, ExitOK)
	if len(ms.Messages) != 1 || ms.Messages[0].Key != "checkout.pay" || ms.Messages[0].Arguments[0].Type != "number" {
		t.Errorf("missing in ja = %+v", ms)
	}
	w.json(&ms, "messages", "--prefix", "cart.").want(t, ExitOK)
	if len(ms.Messages) != 2 {
		t.Errorf("prefix = %+v", ms)
	}
	w.run("messages", "--state", "gone").want(t, ExitUsage)
}

func TestPullWritesApprovedTranslationsDeterministically(t *testing.T) {
	srv, w := seeded(t)
	srv.translations["de"]["cart.checkout"].state = "approved"
	var out pullJSON
	w.json(&out, "pull").want(t, ExitOK)
	if len(out.Locales) != 2 || out.Locales[0].Locale != "de" || out.Locales[0].Messages != 1 || out.Locales[0].Skipped["needs_review"] != 2 {
		t.Fatalf("pull = %+v", out)
	}
	if got := w.read("locales/de.json"); got != "{\n  \"cart.checkout\": \"Zur Kasse\"\n}\n" {
		t.Errorf("de.json = %q", got)
	}
	w.json(&out, "pull", "--states", "all", "--locales", "de").want(t, ExitOK)
	if len(out.Locales) != 1 || out.Locales[0].Messages != 3 || !out.Locales[0].Changed {
		t.Errorf("pull all = %+v", out)
	}
	w.json(&out, "pull", "--states", "all", "--locales", "de").want(t, ExitOK)
	if out.Locales[0].Changed {
		t.Error("an identical pull rewrote the file")
	}
	var doc errorDoc
	w.json(&doc, "pull", "--states", "finished").want(t, ExitUsage)
	_ = json.Valid(nil)
}
