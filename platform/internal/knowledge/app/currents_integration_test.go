//go:build integration

package app_test

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/knowledge/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

// translationIDs maps keys to the ID of their translation into locale.
func translationIDs(t *testing.T, h *harness, project uuid.UUID, locale string, keys ...string) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	for _, k := range keys {
		v, err := h.localization.GetTranslation(h.reviewer(), project, k, locale)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID.UUID())
	}
	return ids
}

// Currents answers what Current answers for each translation — state,
// text and source — in bulk; an unknown ID is absent rather than an
// error, and so is another tenant's translation (forced RLS).
func TestCurrentsMatchesCurrent(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", true, []string{"de", "fr"}, map[string]string{
		"checkout.pay": "Pay {amount, number} now", "checkout.total": "Total",
	})
	h.translate(t, p, "checkout.pay", "de", "Jetzt {amount, number} zahlen", ptr("approved"))
	h.translate(t, p, "checkout.total", "de", "Summe", ptr("draft"))
	h.translate(t, p, "checkout.pay", "fr", "Payer {amount, number}", ptr("approved"))
	// A second tenant's translation is invisible from the first.
	other := harnessFor(t, "globex")
	op := other.project(t, "shop", false, []string{"de"}, map[string]string{"checkout.pay": "Pay"})
	other.translate(t, op, "checkout.pay", "de", "Zahlen", nil)
	foreign := translationIDs(t, other, op, "de", "checkout.pay")[0]

	ids := append(translationIDs(t, h, p, "de", "checkout.pay", "checkout.total"), translationIDs(t, h, p, "fr", "checkout.pay")...)
	missing := uuid.New()

	tr := sources.NewTranslations(h.localization, h.catalog)
	bg, err := authz.Background(tenancy.ContextWithTenant(context.Background(), h.tenant), "knowledge.derive_tm", authz.TranslationsRead, authz.CatalogRead)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.Currents(bg, p, append([]uuid.UUID{missing, foreign}, ids...))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ids) {
		t.Fatalf("Currents returned %d translations, want %d (not the unknown or another tenant's): %v", len(got), len(ids), got)
	}
	for _, id := range ids {
		want, err := tr.Current(bg, p, id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[id], want) {
			t.Errorf("Currents[%s] = %+v, want Current's %+v", id, got[id], want)
		}
	}
	if !got[ids[0]].Approved || got[ids[1]].Approved {
		t.Errorf("approved: pay=%v total=%v, want true, false", got[ids[0]].Approved, got[ids[1]].Approved)
	}

	// The same IDs under the wrong project, and under a project that
	// does not exist, find nothing.
	if got, err := tr.Currents(bg, uuid.New(), ids); err != nil || len(got) != 0 {
		t.Errorf("unknown project: %v, %v; want none", got, err)
	}
	if _, err := tr.Currents(bg, p, nil); err != nil {
		t.Errorf("no ids: %v", err)
	}
}

// countingTranslations counts the reads Knowledge makes of translations.
type countingTranslations struct {
	app.Translations
	current, currents atomic.Int32
}

func (c *countingTranslations) Current(ctx context.Context, project, translation uuid.UUID) (app.CurrentTranslation, error) {
	c.current.Add(1)
	return c.Translations.Current(ctx, project, translation)
}

func (c *countingTranslations) Currents(ctx context.Context, project uuid.UUID, translations []uuid.UUID) (map[uuid.UUID]app.CurrentTranslation, error) {
	c.currents.Add(1)
	return c.Translations.Currents(ctx, project, translations)
}

// Deriving the memory of a batch of translations reads their state in
// bulk: a few reads for the batch, never one per translation (#103).
func TestDeriveTMReadsInBulk(t *testing.T) {
	counter := &countingTranslations{}
	wrapTranslations = func(inner app.Translations) app.Translations {
		counter.Translations = inner
		return counter
	}
	t.Cleanup(func() { wrapTranslations = func(inner app.Translations) app.Translations { return inner } })

	h := newHarness(t)
	const n = 30
	messages := map[string]string{}
	for i := range n {
		messages[fmt.Sprintf("k.%02d", i)] = fmt.Sprintf("Text %02d", i)
	}
	p := h.project(t, "shop", false, []string{"de"}, messages)
	var items []localizationapp.ImportItem
	for k, text := range messages {
		items = append(items, localizationapp.ImportItem{Key: k, Locale: "de", Text: "Deutsch " + text})
	}
	if _, err := h.localization.ImportTranslations(h.reviewer(), p, items); err != nil {
		t.Fatal(err)
	}
	h.drain(t)

	if got := count(t, "SELECT count(*) FROM knowledge_tm_units WHERE retired_at IS NULL"); got != n {
		t.Fatalf("active units = %d, want %d", got, n)
	}
	if c := counter.current.Load(); c != 0 {
		t.Errorf("Current called %d times, want 0: the batch reads in bulk", c)
	}
	if c := counter.currents.Load(); c < 1 || c > n/2 {
		t.Errorf("Currents called %d times for %d translations, want a few", c, n)
	}
}
