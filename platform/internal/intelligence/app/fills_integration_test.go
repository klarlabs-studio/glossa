//go:build integration

package app_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

// selectionProject has a.new (missing in de), b.old (outdated) and
// c.done (up to date).
func selectionProject(t *testing.T, w *wiring) uuid.UUID {
	t.Helper()
	p := w.project(t, []string{"de"}, map[string]string{"a.new": "New", "b.old": "Old", "c.done": "Done"})
	for key, text := range map[string]string{"b.old": "Alt", "c.done": "Fertig"} {
		if _, _, err := w.localization.PutTranslation(w.reviewer(), p, key, "de", localizationapp.TranslationInput{Text: text}, nil); err != nil {
			t.Fatal(err)
		}
	}
	w.push(t, p, map[string]string{"b.old": "Old, revised"})
	return p
}

// fillKeys requests a fill and returns the keys of its jobs, sorted.
// It cancels them again, so the next fill queues them anew (under its
// own ID).
func fillKeys(t *testing.T, w *wiring, p uuid.UUID, f app.FillFilter) ([]string, app.FillResult) {
	t.Helper()
	res, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}, Filter: f}, "")
	if err != nil {
		t.Fatal(err)
	}
	jobs, _, err := w.svc.ListJobs(w.developer(), app.JobFilter{FillID: &res.Fill.ID}, firstPageW())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.CancelFill(w.developer(), res.Fill.ID); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, j := range jobs {
		keys = append(keys, j.MessageKey)
	}
	slices.Sort(keys)
	return keys, res
}

// A fill selects messages by their translation's state — missing (the
// default), outdated, or both — with or without listed keys.
func TestWiringFillSelectsByState(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 0)
	p := selectionProject(t, w)

	for _, tc := range []struct {
		name   string
		filter app.FillFilter
		want   string
	}{
		{"default", app.FillFilter{}, "a.new"},
		{"missing", app.FillFilter{Select: app.SelectMissing}, "a.new"},
		{"outdated", app.FillFilter{Select: app.SelectOutdated}, "b.old"},
		{"both", app.FillFilter{Select: app.SelectMissingOrOutdated}, "a.new,b.old"},
		{"include_outdated", app.FillFilter{IncludeOutdated: true}, "a.new,b.old"},
		{"keys", app.FillFilter{Keys: []string{"a.new", "b.old", "c.done"}}, "a.new,b.old"},
		{"outdated keys", app.FillFilter{Keys: []string{"a.new", "b.old", "c.done"}, Select: app.SelectOutdated}, "b.old"},
		{"prefix", app.FillFilter{KeyPrefix: "a.", Select: app.SelectMissingOrOutdated}, "a.new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, res := fillKeys(t, w, p, tc.filter)
			if strings.Join(keys, ",") != tc.want {
				t.Errorf("keys = %v, want %s (fill %+v)", keys, tc.want, res.Fill)
			}
			if res.Fill.Filter.Select == "" {
				t.Errorf("the effective select isn't recorded: %+v", res.Fill.Filter)
			}
		})
	}
	_, res := fillKeys(t, w, p, app.FillFilter{Keys: []string{"a.new", "b.old", "c.done"}, Select: app.SelectOutdated})
	if res.Fill.Skipped[domain.SkipUpToDate] != 1 || res.Fill.Skipped[app.SkipNotSelected] != 1 {
		t.Errorf("skipped = %v, want c.done up to date and a.new not selected", res.Fill.Skipped)
	}

	for _, bad := range []app.FillFilter{
		{Select: "everything"},
		{Select: app.SelectMissing, IncludeOutdated: true},
	} {
		if _, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}, Filter: bad}, ""); !errors.Is(err, app.ErrInvalidQuery) {
			t.Errorf("filter %+v: err = %v, want ErrInvalidQuery", bad, err)
		}
	}
}

// previewProject has order.save (an exact TM hit of cart.save's
// approved de text), home.title (needs a provider) and terms.body in a
// sensitive namespace.
func previewProject(t *testing.T, w *wiring) uuid.UUID {
	t.Helper()
	p := w.project(t, []string{"de"}, map[string]string{
		"cart.save": "Save your changes", "order.save": "Save your changes", "home.title": "Welcome home",
	})
	if _, _, err := w.localization.PutTranslation(w.reviewer(), p, "cart.save", "de", localizationapp.TranslationInput{Text: "Änderungen speichern"}, nil); err != nil {
		t.Fatal(err)
	}
	legal := "legal"
	if _, err := w.catalog.UpsertMessages(w.developer(), catalogdomain.ProjectID(p), []catalogapp.UpsertItem{
		{Key: "terms.body", Namespace: &legal, Text: "You agree to the terms"},
	}); err != nil {
		t.Fatal(err)
	}
	tags := domain.NamespaceTags{"legal": {domain.TagSensitive}}
	if _, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{NamespaceTags: &tags}, nil); err != nil {
		t.Fatal(err)
	}
	w.drain(t) // Knowledge derives the TM unit
	return p
}

// sideEffects counts every row a fill, a job, a TM hit, a call or an
// event would leave.
func sideEffects(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("fills=%d jobs=%d spend=%d disclosures=%d events=%d hits=%d",
		wcount(t, "SELECT count(*) FROM intelligence_fills"), wcount(t, "SELECT count(*) FROM intelligence_jobs"),
		wcount(t, "SELECT count(*) FROM intelligence_spend"), wcount(t, "SELECT count(*) FROM intelligence_disclosures"),
		wcount(t, "SELECT count(*) FROM outbox_events"), wcount(t, "SELECT coalesce(sum(hit_count), 0)::int FROM knowledge_tm_units"))
}

func preview(t *testing.T, w *wiring, p uuid.UUID, f app.FillFilter) app.LocalePreview {
	t.Helper()
	before := sideEffects(t)
	pv, err := w.svc.PreviewFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}, Filter: f})
	if err != nil {
		t.Fatal(err)
	}
	if after := sideEffects(t); after != before {
		t.Fatalf("a preview wrote something: %s → %s", before, after)
	}
	if len(pv.Locales) != 1 || pv.Locales[0].Locale != "de" || pv.Select == "" {
		t.Fatalf("preview = %+v", pv)
	}
	return pv.Locales[0]
}

// A preview says what a fill would queue and what would happen to each
// job — without writing anything.
func TestWiringFillPreviewWritesNothing(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 5_000_000)
	p := previewProject(t, w)

	// Consent off: the TM hit is still reused, the rest is refused.
	lp := preview(t, w, p, app.FillFilter{})
	if strings.Join(lp.Keys, ",") != "home.title,order.save" || lp.TMExact != 1 || lp.Provider != 0 ||
		lp.Refused[app.RefusedProviderConsent] != 1 || lp.Refused[app.RefusedSensitive] != 1 || lp.Cost.Estimated != 0 {
		t.Errorf("consent off = %+v", lp)
	}

	// Consent on: home.title would call the provider, at a price.
	consent := true
	if _, err := w.svc.PutSettings(w.admin(), app.SettingsInput{ProviderConsent: &consent}, nil); err != nil {
		t.Fatal(err)
	}
	lp = preview(t, w, p, app.FillFilter{})
	if lp.Provider != 1 || lp.TMExact != 1 || len(lp.Refused) != 1 || lp.Cost.Estimated <= 0 || lp.Cost.Max < lp.Cost.Estimated || lp.Cost.Unpriced {
		t.Errorf("consent on = %+v", lp)
	}

	// Too little budget for one call's upper bound.
	budget := domain.MicroUSD(1_000)
	if _, err := w.svc.PutSettings(w.admin(), app.SettingsInput{MonthlyBudget: &budget}, nil); err != nil {
		t.Fatal(err)
	}
	if lp = preview(t, w, p, app.FillFilter{}); lp.Refused[app.RefusedBudgetExceeded] != 1 || lp.Provider != 0 {
		t.Errorf("small budget = %+v", lp)
	}

	// No route: the only provider is disabled.
	budget = 5_000_000
	if _, err := w.svc.PutSettings(w.admin(), app.SettingsInput{MonthlyBudget: &budget}, nil); err != nil {
		t.Fatal(err)
	}
	providers, _, err := w.svc.ListProviders(w.admin(), firstPageW())
	if err != nil || len(providers) != 1 {
		t.Fatalf("providers = %+v, %v", providers, err)
	}
	off := false
	v := providers[0].Version
	if _, err := w.svc.UpdateProvider(w.admin(), providers[0].ID, &v, app.ProviderPatch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if lp = preview(t, w, p, app.FillFilter{}); lp.Refused[app.RefusedNoRoute] != 1 {
		t.Errorf("no route = %+v", lp)
	}

	// A fill's jobs exist afterwards: a preview counts them as reused.
	if _, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, ""); err != nil {
		t.Fatal(err)
	}
	if lp = preview(t, w, p, app.FillFilter{}); lp.Existing != 2 || lp.TMExact != 0 || len(lp.Keys) != 2 {
		t.Errorf("after the fill = %+v", lp)
	}

	// The same checks as a fill.
	if _, err := w.svc.PreviewFill(w.developer(), p, app.FillRequest{Locales: []string{"fr"}}); !errors.Is(err, app.ErrLocaleNotFound) {
		t.Errorf("unknown locale: %v", err)
	}
	if _, err := w.svc.PreviewFill(w.as([]string{"translator"}, "fr"), p, app.FillRequest{Locales: []string{"de"}}); err == nil {
		t.Error("a translator for fr previewed a de fill")
	}
}

// Suggestions come with their message's current source — key,
// namespace, authored text, canonical MF2 and revision — read in one
// Catalog query per project for a whole page.
func TestWiringSuggestionSources(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 0)
	p := w.project(t, []string{"de"}, map[string]string{"cart.save": "Save {count, number} changes", "order.save": "Save {count, number} changes"})
	if _, _, err := w.localization.PutTranslation(w.reviewer(), p, "cart.save", "de", localizationapp.TranslationInput{Text: "{count, number} Änderungen speichern"}, nil); err != nil {
		t.Fatal(err)
	}
	w.drain(t)
	if _, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, ""); err != nil {
		t.Fatal(err)
	}
	w.work(t)
	queue, _, err := w.svc.ReviewQueue(w.reviewer(), p, []string{"de"}, firstPageW())
	if err != nil || len(queue) != 1 || queue[0].MessageKey != "order.save" {
		t.Fatalf("queue = %+v, %v", queue, err)
	}
	w.push(t, p, map[string]string{"order.save": "Save all {count, number} changes"})

	sources, err := w.svc.SuggestionSources(w.reviewer(), queue)
	if err != nil {
		t.Fatal(err)
	}
	src, ok := sources[queue[0].MessageID]
	if !ok || src.Key != "order.save" || src.Namespace != "default" || !src.Active || src.Revision != 2 ||
		src.Text != "Save all {count, number} changes" || src.Syntax != "mf1" || src.MF2 != "Save all {$count :number} changes" {
		t.Errorf("source = %+v", src)
	}
	if empty, err := w.svc.SuggestionSources(w.reviewer(), nil); err != nil || len(empty) != 0 {
		t.Errorf("no suggestions = %+v, %v", empty, err)
	}
}
