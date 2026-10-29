package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

func tag(s string) bcp47.Tag { return bcp47.MustParse(s) }

func content(t *testing.T, syntax mfcontent.Syntax, text, locale string) mfcontent.Content {
	t.Helper()
	c, err := mfcontent.Parse(syntax, text, tag(locale))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConceptFromFileKeepsOneDefinitionAndFoldsTheRestIntoNotes(t *testing.T) {
	tb := formats.Termbase{Language: tag("en")}
	c := formats.Concept{
		ID: "c1", Domain: "billing",
		Definitions: []formats.Definition{{Locale: tag("de"), Text: "Eine Rechnung"}, {Locale: tag("en"), Text: "A bill"}},
		Notes:       []string{"Legal term."},
		Terms: []formats.Term{
			{Locale: tag("en"), Text: "invoice", Status: formats.TermPreferred, PartOfSpeech: "noun", Context: "Pay the invoice."},
			{Locale: tag("de"), Text: "Faktura", Notes: []string{"Austrian"}},
		},
	}
	w := app.ConceptFromFile(tb, c)
	if w.Definition != "A bill" || w.Domain != "billing" {
		t.Errorf("definition = %q, domain = %q", w.Definition, w.Domain)
	}
	if w.Note != "Legal term.\n\nDefinition (de): Eine Rechnung" {
		t.Errorf("note = %q", w.Note)
	}
	if len(w.Terms) != 2 || w.Terms[0].Status != "preferred" || w.Terms[0].Note != "Context: Pay the invoice." ||
		w.Terms[1].Status != "admitted" || w.Terms[1].Note != "Austrian" || w.Terms[0].PartOfSpeech != "noun" {
		t.Errorf("terms = %+v", w.Terms)
	}
	// Without a definition in the document language or without a locale,
	// the first one wins; one without a locale beats the language.
	c.Definitions = []formats.Definition{{Locale: tag("de"), Text: "x"}, {Text: "neutral"}}
	if got := app.ConceptFromFile(tb, c).Definition; got != "neutral" {
		t.Errorf("definition = %q", got)
	}
}

func TestConceptRoundTripThroughTheExchangeModel(t *testing.T) {
	id := uuid.MustParse("0192a1b2-0000-7000-8000-000000000001")
	view := app.ConceptView{ID: id, Definition: "A bill", Domain: "billing", Note: "Legal term.", Terms: []app.TermWrite{
		{Locale: "en", Text: "invoice", Status: "preferred", PartOfSpeech: "noun", Note: "Use in UI."},
		{Locale: "de", Text: "Rechnung", Status: "forbidden"},
	}}
	fc := app.ConceptToFile(view)
	if fc.ID != id.String() || len(fc.Definitions) != 1 || fc.Terms[1].Status != formats.TermForbidden {
		t.Fatalf("file concept = %+v", fc)
	}
	back := app.ConceptFromFile(formats.Termbase{Language: tag("en")}, fc)
	if back.Definition != view.Definition || back.Note != view.Note || back.Domain != view.Domain ||
		len(back.Terms) != 2 || back.Terms[0] != view.Terms[0] || back.Terms[1] != view.Terms[1] {
		t.Errorf("round trip = %+v, want %+v", back, view)
	}
}

func TestTMUnitToFile(t *testing.T) {
	hit := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	u, err := app.TMUnitToFile(app.TMUnitView{
		ID: uuid.MustParse("0192a1b2-0000-7000-8000-000000000002"), MessageKey: "checkout.pay",
		SourceLocale: tag("en"), TargetLocale: tag("de"), SourceMF2: "Pay {$amount}", TargetMF2: "{$amount} zahlen",
		HitCount: 3, LastHitAt: &hit, CreatedAt: hit, UpdatedAt: hit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.Source.Text != "Pay {$amount}" || u.Target.Text != "{$amount} zahlen" || u.UsageCount != 3 || !u.LastUsedAt.Equal(hit) ||
		len(u.Props) != 1 || u.Props[0].Value != "checkout.pay" {
		t.Errorf("unit = %+v", u)
	}
	if _, err := app.TMUnitToFile(app.TMUnitView{SourceLocale: tag("en"), TargetLocale: tag("de"), SourceMF2: "{", TargetMF2: "x"}); err == nil {
		t.Error("a malformed stored unit must fail")
	}
}

func TestCatalogFromSnapshotFiltersNamespacesAndOrdersTargets(t *testing.T) {
	snap := app.Snapshot{
		Project: app.ProjectInfo{SourceLocale: tag("en")},
		Messages: []app.SnapshotMessage{
			{Key: "a", Namespace: "web", Source: content(t, mfcontent.MF1, "A", "en"), MaxLength: 10, Description: "d",
				Translations: map[bcp47.Tag]app.SnapshotTranslation{
					tag("fr"): {Content: content(t, mfcontent.MF1, "A-fr", "fr"), State: "approved"},
					tag("de"): {Content: content(t, mfcontent.MF1, "A-de", "de"), State: "needs_review"},
				}},
			{Key: "b", Namespace: "app", Source: content(t, mfcontent.MF1, "B", "en")},
		},
	}
	all := app.CatalogFromSnapshot(snap, nil)
	if len(all.Entries) != 2 || all.SourceLocale != tag("en") {
		t.Fatalf("catalog = %+v", all)
	}
	e := all.Entries[0]
	if e.MaxLength != 10 || e.Description != "d" || len(e.Targets) != 2 || e.Targets[0].Locale != tag("de") ||
		e.Targets[0].State != formats.StateNeedsReview {
		t.Errorf("entry = %+v", e)
	}
	web := app.CatalogFromSnapshot(snap, []string{"web"})
	if len(web.Entries) != 1 || !strings.EqualFold(web.Entries[0].ID, "a") {
		t.Errorf("namespace filter = %+v", web.Entries)
	}
}
