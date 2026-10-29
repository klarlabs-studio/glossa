//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

var shop = map[string]string{
	"checkout.pay": "Pay {amount, number}",
	"cart.items":   "{count, plural, one {# item} other {# items}}",
	"home.title":   "Welcome",
}

func TestLocalesAndDirection(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de_de", "ar"}, nil)
	ctx := h.developer()

	if _, created, err := h.svc.AddLocale(ctx, p, "de-DE"); err != nil || created {
		t.Errorf("re-adding de-DE: created=%v err=%v", created, err)
	}
	if _, _, err := h.svc.AddLocale(ctx, p, "en-u-nu-arab"); !errors.Is(err, bcp47.ErrInvalid) {
		t.Errorf("extension tag: %v", err)
	}
	ls, _, err := h.svc.ListLocales(ctx, p, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range ls {
		got[l.Code.String()] = string(l.Direction())
		if l.IsSource != (l.Code.String() == "en") {
			t.Errorf("%s is_source = %v", l.Code, l.IsSource)
		}
	}
	if len(got) != 3 || got["ar"] != "rtl" || got["de-DE"] != "ltr" || got["en"] != "ltr" {
		t.Errorf("locales = %v", got)
	}
	if err := h.svc.RemoveLocale(ctx, p, "en"); !errors.Is(err, domain.ErrSourceLocale) {
		t.Errorf("remove source: %v", err)
	}
	if _, err := h.svc.PutFallbackGraph(ctx, p, map[string][]string{"ar": {"en"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RemoveLocale(ctx, p, "ar"); !errors.Is(err, app.ErrLocaleInFallback) {
		t.Errorf("remove locale in fallback: %v", err)
	}
	if err := h.svc.RemoveLocale(ctx, p, "de-DE"); err != nil {
		t.Errorf("remove de-DE: %v", err)
	}
	if err := h.svc.RemoveLocale(ctx, p, "fr"); !errors.Is(err, app.ErrLocaleNotFound) {
		t.Errorf("remove unknown: %v", err)
	}
	if _, _, err := h.svc.AddLocale(h.as([]string{"translator"}), p, "fr"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator adds locale: %v", err)
	}
}

func TestFallbackGraphConcurrencyAndValidation(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de", "de-AT", "de-CH"}, nil)
	ctx := h.developer()

	g, err := h.svc.FallbackGraph(ctx, p)
	if err != nil || g.Version != 0 || len(g.Edges) != 0 {
		t.Fatalf("empty graph: %+v %v", g, err)
	}
	if _, err := h.svc.PutFallbackGraph(ctx, p, map[string][]string{"de-AT": {"de"}}, ptr(1)); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("If-Match on a missing graph: %v", err)
	}
	g, err = h.svc.PutFallbackGraph(ctx, p, map[string][]string{"de_at": {"de-CH", "de"}, "*": {"en"}}, nil)
	if err != nil || g.Version != 1 || g.Edges["de-AT"][0] != "de-CH" {
		t.Fatalf("first put: %+v %v", g, err)
	}
	if _, err := h.svc.PutFallbackGraph(ctx, p, map[string][]string{}, nil); !errors.Is(err, app.ErrPreconditionRequired) {
		t.Errorf("replace without If-Match: %v", err)
	}
	var fe *domain.FallbackError
	if _, err := h.svc.PutFallbackGraph(ctx, p, map[string][]string{"de": {"de-AT"}, "de-AT": {"de"}}, ptr(1)); !errors.As(err, &fe) || fe.Code != "fallback_cycle" {
		t.Errorf("cycle: %v", err)
	}
	if _, err := h.svc.PutFallbackGraph(ctx, p, map[string][]string{"fr": {"en"}}, ptr(1)); !errors.As(err, &fe) || fe.Code != "fallback_unknown_locale" {
		t.Errorf("unknown locale: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.fallback_graph.changed'"); n != 1 {
		t.Errorf("fallback events = %d", n)
	}
}

// The core loop's heart: a source revision flows through the outbox and
// makes the translation outdated — derived, never stored.
func TestSourceRevisionMakesTranslationsOutdated(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de", "fr"}, shop)
	ctx := h.developer()

	de, status, err := h.svc.PutTranslation(ctx, p, "cart.items", "de", app.TranslationInput{
		Text: "{count, plural, one {# Artikel} other {# Artikel}}",
	}, nil)
	if err != nil || status != app.WriteCreated {
		t.Fatalf("translate de: %v %s", err, status)
	}
	if de.State != domain.StateApproved || de.Outdated() || de.SourceRevision != 1 || de.Origin != domain.OriginHuman {
		t.Errorf("de = %+v", de)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "cart.items", "fr", app.TranslationInput{
		Text: "{count, plural, one {# article} other {# articles}}",
	}, nil); err != nil {
		t.Fatal(err)
	}

	m, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), "cart.items")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.catalog.ReviseSource(ctx, catalogdomain.ProjectID(p), "cart.items", m.Version,
		"{count, plural, one {# product} other {# products}}", ""); err != nil {
		t.Fatal(err)
	}
	// Before delivery Localization hasn't seen the revision yet…
	if got, _ := h.svc.GetTranslation(ctx, p, "cart.items", "de"); got.Outdated() {
		t.Error("outdated before the event was delivered")
	}
	h.drain(t)
	// …after it, both translations are outdated, and nothing was stored
	// on them to say so.
	for _, l := range []string{"de", "fr"} {
		got, err := h.svc.GetTranslation(ctx, p, "cart.items", l)
		if err != nil || !got.Outdated() || got.CurrentSourceRevision != 2 || got.SourceRevision != 1 {
			t.Errorf("%s after revision: %+v %v", l, got, err)
		}
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated'"); n != 2 {
		t.Errorf("outdated events = %d, want 2", n)
	}
	// The projection caught up on its own: Localization's subscriber,
	// not the person who revised the source, marked them outdated.
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated' AND actor = $1",
		authz.SystemEventActor("localization.track_message").String()); n != 2 {
		t.Errorf("outdated events naming the projection = %d, want 2", n)
	}
	ids, err := h.svc.MessagesWithCoverage(ctx, p, app.CoverageFilter{Locale: "de", Outdated: true, Limit: 10})
	if err != nil || len(ids) != 1 || ids[0] != m.ID.UUID() {
		t.Errorf("outdated in de: %v %v", ids, err)
	}
	// Catalog's message list filters through the same port.
	list, _, err := h.catalog.ListMessages(ctx, catalogdomain.ProjectID(p), catalogapp.MessageQuery{MissingIn: "de"}, firstPage())
	if err != nil || len(list) != 2 {
		t.Errorf("missing in de: %d %v", len(list), err)
	}

	// Confirming the unchanged text against revision 2 makes it current.
	cur, status, err := h.svc.PutTranslation(ctx, p, "cart.items", "de", app.TranslationInput{
		Text: "{count, plural, one {# Artikel} other {# Artikel}}",
	}, ptr(de.Revision))
	if err != nil || status != app.WriteRevised || cur.Outdated() || cur.SourceRevision != 2 {
		t.Errorf("confirm: %+v %s %v", cur, status, err)
	}
	// Redelivering the source event changes nothing (idempotent).
	if _, err := env.Super.Exec(context.Background(), `UPDATE outbox_events SET status = 'pending', delivered_to = '{}',
		available_at = now() WHERE event_type LIKE 'catalog.message.%'`); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'localization.translation.outdated'"); n != 2 {
		t.Errorf("outdated events after redelivery = %d, want still 2", n)
	}
	if n := count(t, "SELECT source_revision FROM localization_messages WHERE message_id = $1", m.ID.UUID()); n != 2 {
		t.Errorf("projected revision = %d", n)
	}
}

func TestPutTranslationConcurrency(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de"}, shop)
	ctx := h.developer()
	if _, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, ptr(1)); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("If-Match on create: %v", err)
	}
	tr, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.State != domain.StateNeedsReview {
		t.Errorf("state with review required = %s", tr.State)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Hallo"}, nil); !errors.Is(err, app.ErrPreconditionRequired) {
		t.Errorf("replace without If-Match: %v", err)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Hallo"}, ptr(tr.Revision+1)); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale If-Match: %v", err)
	}
	if _, status, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, ptr(tr.Revision)); err != nil || status != app.WriteUnchanged {
		t.Errorf("same text: %s %v", status, err)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "home.title", "en", app.TranslationInput{Text: "x"}, nil); !errors.Is(err, domain.ErrSourceLocale) {
		t.Errorf("source locale: %v", err)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "home.title", "fr", app.TranslationInput{Text: "x"}, nil); !errors.Is(err, app.ErrLocaleNotFound) {
		t.Errorf("locale not in project: %v", err)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "nope", "de", app.TranslationInput{Text: "x"}, nil); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown message: %v", err)
	}
}

func TestStructuralQAGateOnWrite(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, shop)
	ctx := h.developer()
	var qa *domain.QAError
	_, _, err := h.svc.PutTranslation(ctx, p, "checkout.pay", "de", app.TranslationInput{Text: "Jetzt bezahlen"}, nil)
	if !errors.As(err, &qa) || qa.Findings[0].Code != mf.FindingMissingArgument {
		t.Fatalf("missing argument accepted: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM localization_translations"); n != 0 {
		t.Errorf("rejected write stored %d translations", n)
	}
	// Warnings are stored and returned.
	limit := 3
	m, err := h.catalog.GetMessage(ctx, catalogdomain.ProjectID(p), "home.title")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.catalog.UpdateMessage(ctx, catalogdomain.ProjectID(p), "home.title", m.Version, catalogapp.MessageChange{MaxLength: &limit}); err != nil {
		t.Fatal(err)
	}
	tr, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil)
	if err != nil || len(tr.Warnings) != 1 || tr.Warnings[0].Code != domain.FindingMaxLengthExceeded {
		t.Errorf("warnings: %+v %v", tr.Warnings, err)
	}
	stored, err := h.svc.GetTranslation(ctx, p, "home.title", "de")
	if err != nil || len(stored.Warnings) != 1 {
		t.Errorf("stored warnings: %+v %v", stored.Warnings, err)
	}
}

func TestTranslatorLocaleScopingAndReview(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de", "de-AT", "fr"}, shop)
	translator := h.as([]string{"translator"}, "de")
	reviewer := h.as([]string{"reviewer"}, "de")

	tr, _, err := h.svc.PutTranslation(translator, p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The de scope covers de-AT (its CLDR descendant), not fr.
	if _, _, err := h.svc.PutTranslation(translator, p, "home.title", "de-AT", app.TranslationInput{Text: "Servus"}, nil); err != nil {
		t.Errorf("translator in de-AT: %v", err)
	}
	if _, _, err := h.svc.PutTranslation(translator, p, "home.title", "fr", app.TranslationInput{Text: "Bienvenue"}, nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator in fr: %v", err)
	}
	if _, _, err := h.svc.PutTranslation(translator, p, "home.title", "de", app.TranslationInput{Text: "Hallo", State: ptr("approved")}, ptr(tr.Revision)); !errors.Is(err, domain.ErrReviewForbidden) {
		t.Errorf("translator approves on write: %v", err)
	}
	if _, err := h.svc.ReviewTranslation(translator, p, "home.title", "de", "approved", tr.Revision); !errors.Is(err, domain.ErrReviewForbidden) {
		t.Errorf("translator reviews: %v", err)
	}
	approved, err := h.svc.ReviewTranslation(reviewer, p, "home.title", "de", "approved", tr.Revision)
	if err != nil || approved.State != domain.StateApproved || approved.Revision != 2 {
		t.Fatalf("review: %+v %v", approved, err)
	}
	// Provenance survives review; the log names both.
	revs, _, err := h.svc.TranslationRevisions(reviewer, p, "home.title", "de", firstPage())
	if err != nil || len(revs) != 2 {
		t.Fatalf("history: %v %d", err, len(revs))
	}
	if revs[0].Kind != domain.KindReview || revs[1].Kind != domain.KindContent || revs[0].Provenance.By == revs[1].Provenance.By {
		t.Errorf("history = %+v", revs)
	}
	if approved.Origin != domain.OriginHuman || approved.By != revs[1].Provenance.By {
		t.Errorf("current provenance = %s by %s", approved.Origin, approved.By)
	}
	if _, err := h.svc.ReviewTranslation(h.as([]string{"reviewer"}, "fr"), p, "home.title", "de", "rejected", 2); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("fr reviewer in de: %v", err)
	}
	// The revision log is append-only for the application role.
	if _, err := env.App.Exec(context.Background(), "DELETE FROM localization_translation_revisions"); err == nil {
		t.Error("glossa_app may delete translation history")
	}
}

func TestBulkImport(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de", "fr"}, shop)
	token := authztest.Token(context.Background(), h.tenant, "write")
	items := []app.ImportItem{
		{Key: "home.title", Locale: "de", Text: "Willkommen", OriginDetail: json.RawMessage(`{"source":"v0.3","job":"j1"}`)},
		{Key: "cart.items", Locale: "fr", Text: "{count, plural, one {# article} other {# articles}}"},
		{Key: "checkout.pay", Locale: "de", Text: "Bezahlen"},  // QA error: missing {amount}
		{Key: "nope", Locale: "de", Text: "x"},                 // unknown message
		{Key: "home.title", Locale: "it", Text: "Benvenuto"},   // not a project locale
		{Key: "home.title", Locale: "en", Text: "Welcome"},     // source locale
		{Key: "home.title", Locale: "de", Text: "Willkommen!"}, // duplicate slot
	}
	res, err := h.svc.ImportTranslations(token, p, items)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "", "structural_qa_failed", "message_not_found", "locale_not_found", "source_locale", "duplicate_item"}
	for i, w := range want {
		code := ""
		if res[i].Error != nil {
			code = res[i].Error.Code
		}
		if code != w {
			t.Errorf("item %d: %q (%+v), want %q", i, code, res[i].Error, w)
		}
	}
	if res[0].Status != app.WriteCreated || res[0].Translation.Origin != domain.OriginImport {
		t.Errorf("imported: %+v", res[0])
	}
	if len(res[2].Error.Findings) == 0 {
		t.Error("QA failure without findings")
	}
	again, err := h.svc.ImportTranslations(token, p, items[:2])
	if err != nil || again[0].Status != app.WriteUnchanged || again[1].Status != app.WriteUnchanged {
		t.Errorf("re-import: %+v %v", again, err)
	}
	var detail string
	if err := env.Super.QueryRow(context.Background(), `SELECT origin_detail->>'job' FROM localization_translation_revisions
		WHERE origin = 'import' ORDER BY created_at LIMIT 1`).Scan(&detail); err != nil || detail != "j1" {
		t.Errorf("provenance detail = %q %v", detail, err)
	}
	// A translator limited to fr imports only fr.
	scoped, err := h.svc.ImportTranslations(h.as([]string{"translator"}, "fr"), p, []app.ImportItem{
		{Key: "home.title", Locale: "fr", Text: "Bienvenue"}, {Key: "home.title", Locale: "de", Text: "Hallo"},
	})
	if err != nil || scoped[0].Error != nil || scoped[1].Error == nil || scoped[1].Error.Code != "forbidden" {
		t.Errorf("scoped import: %+v %v", scoped, err)
	}
}

// Import jobs merge without losing approvals and preview without
// writing.
func TestImportKeepsApprovalsAndDryRunsWriteNothing(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, shop)
	owner := h.as([]string{"owner"})
	approved, needsReview := "approved", "needs_review"
	if _, err := h.svc.ImportTranslations(owner, p, []app.ImportItem{
		{Key: "home.title", Locale: "de", Text: "Willkommen", State: &approved},
		{Key: "cart.items", Locale: "de", Text: "{count, plural, one {# Artikel} other {# Artikel}}", State: &needsReview},
	}); err != nil {
		t.Fatal(err)
	}
	keep := app.ImportOptions{KeepApproved: true}
	res, err := h.svc.ImportTranslationsWith(owner, p, []app.ImportItem{
		{Key: "home.title", Locale: "de", Text: "Hallo", State: &approved},          // other text: conflict
		{Key: "cart.items", Locale: "de", Text: "{count, plural, other {# Stück}}"}, // not approved: revised
		{Key: "home.title", Locale: "de", Text: "Willkommen", State: &needsReview},  // duplicate slot
	}, keep)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Error == nil || res[0].Error.Code != "approved_translation_conflict" || res[1].Status != app.WriteRevised {
		t.Errorf("merge: %+v %+v", res[0], res[1])
	}
	lower, err := h.svc.ImportTranslationsWith(owner, p, []app.ImportItem{
		{Key: "home.title", Locale: "de", Text: "Willkommen", State: &needsReview}, // same text, lower state
	}, keep)
	if err != nil || lower[0].Status != app.WriteUnchanged || lower[0].Error != nil {
		t.Errorf("same text in a lower state must keep the approval: %+v %v", lower[0], err)
	}
	if tr, _ := h.svc.GetTranslation(owner, p, "home.title", "de"); tr.State != domain.StateApproved || tr.Content.Text != "Willkommen" {
		t.Errorf("approved translation changed: %+v", tr.Translation)
	}

	events := count(t, "SELECT count(*) FROM outbox_events")
	revisions := count(t, "SELECT count(*) FROM localization_translation_revisions")
	dry, err := h.svc.ImportTranslationsWith(owner, p, []app.ImportItem{
		{Key: "checkout.pay", Locale: "de", Text: "{amount, number} zahlen"},
		{Key: "home.title", Locale: "de", Text: "Hallo"},
	}, app.ImportOptions{DryRun: true})
	if err != nil || dry[0].Status != app.WriteCreated || dry[1].Status != app.WriteRevised {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if _, err := h.svc.GetTranslation(owner, p, "checkout.pay", "de"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a dry run wrote a translation: %v", err)
	}
	if count(t, "SELECT count(*) FROM outbox_events") != events || count(t, "SELECT count(*) FROM localization_translation_revisions") != revisions {
		t.Error("a dry run published events or appended revisions")
	}
}

func TestReleaseSnapshot(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de", "ar"}, shop)
	ctx := h.developer()
	reviewer := h.as([]string{"reviewer"})
	tr, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReviewTranslation(reviewer, p, "home.title", "de", "approved", tr.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.PutTranslation(ctx, p, "home.title", "ar", app.TranslationInput{Text: "أهلا"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PutFallbackGraph(ctx, p, map[string][]string{"*": {"en"}}, nil); err != nil {
		t.Fatal(err)
	}
	snap, err := h.svc.ReleaseTranslations(ctx, p, []domain.ReviewState{domain.StateApproved})
	if err != nil {
		t.Fatal(err)
	}
	if snap.SourceLocale.String() != "en" || len(snap.Locales) != 3 || snap.Fallback["*"][0] != "en" {
		t.Errorf("snapshot header: %+v", snap)
	}
	dirs := map[string]bcp47.Direction{}
	for _, l := range snap.Locales {
		dirs[l.Code.String()] = l.Direction
	}
	if dirs["ar"] != bcp47.RTL || dirs["de"] != bcp47.LTR {
		t.Errorf("directions = %v", dirs)
	}
	if len(snap.Translations[bcp47.MustParse("de")]) != 1 || len(snap.Translations[bcp47.MustParse("ar")]) != 0 {
		t.Errorf("approved translations = %v", snap.Translations)
	}
	src, err := h.catalog.ReleaseSource(ctx, catalogdomain.ProjectID(p))
	if err != nil || len(src.Messages) != 3 {
		t.Fatalf("release source: %v", err)
	}
}

func TestLocalizationIsTenantIsolated(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, shop)
	if _, _, err := h.svc.PutTranslation(h.developer(), p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil); err != nil {
		t.Fatal(err)
	}
	other, err := env.SeedTenant(context.Background(), "bolt")
	if err != nil {
		t.Fatal(err)
	}
	intruder := authztest.Member(context.Background(), other, []string{"owner"})
	if _, err := h.svc.GetTranslation(intruder, p, "home.title", "de"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("translation: %v", err)
	}
	if _, _, err := h.svc.ListLocales(intruder, p, firstPage()); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("locales: %v", err)
	}
	if _, err := h.svc.FallbackGraph(intruder, p); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("fallback graph: %v", err)
	}
	if _, _, err := h.svc.PutTranslation(intruder, p, "home.title", "de", app.TranslationInput{Text: "pwned"}, nil); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("write: %v", err)
	}
	if _, err := h.svc.ImportTranslations(intruder, p, []app.ImportItem{{Key: "home.title", Locale: "de", Text: "x"}}); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("import: %v", err)
	}
	// Even reading by id through the store: the other tenant sees no rows.
	ids, err := h.svc.MessagesWithCoverage(intruder, p, app.CoverageFilter{Locale: "de", Limit: 10})
	if err != nil || len(ids) != 0 {
		t.Errorf("coverage: %v %v", ids, err)
	}
	for _, table := range []string{"localization_locales", "localization_translations", "localization_translation_revisions", "localization_messages"} {
		var n int
		tx, err := env.App.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Errorf("unscoped read of %s: %d %v", table, n, err)
		}
		_ = tx.Rollback(context.Background())
	}
}

func TestProjectDeletionDropsLocalizationData(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, shop)
	if _, _, err := h.svc.PutTranslation(h.developer(), p, "home.title", "de", app.TranslationInput{Text: "Willkommen"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.catalog.DeleteProject(h.as([]string{"admin"}), catalogdomain.ProjectID(p), nil); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	for _, table := range []string{"localization_locales", "localization_translations", "localization_translation_revisions", "localization_messages"} {
		if n := count(t, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s keeps %d rows", table, n)
		}
	}
}
