//go:build integration

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
)

// outcome names how a restricted call ended: a thing outside the
// caller's restriction must be "not found", exactly as one that does
// not exist (RFC 0006 §3.3, §4.1).
func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound), errors.Is(err, catalogapp.ErrNotFound):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestLocalizationRestrictions: a project outside a principal's scope
// is not found in every read and write, and an assigned member reads
// and writes only the units their assignments cover — never review,
// import, stats or another unit.
func TestLocalizationRestrictions(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de", "fr"}, map[string]string{"a": "Apple", "b": "Banana"})
	dev := h.developer()
	for _, u := range [][2]string{{"a", "de"}, {"a", "fr"}, {"b", "de"}} {
		if _, _, err := h.svc.PutTranslation(dev, p, u[0], u[1], app.TranslationInput{Text: u[0] + " " + u[1]}, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, err := h.catalog.GetMessage(dev, catalogdomain.ProjectID(p), "a")
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	scoped := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{other}, []string{"translator"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de", "fr")
	cov.Assign(member, p, a.ID.UUID(), "de")
	revision := 1

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: get out of scope", func() error { _, err := h.svc.GetTranslation(scoped, p, "a", "de"); return err }, "not found"},
		{"scoped: list out of scope", func() error {
			_, _, err := h.svc.ListProjectTranslations(scoped, p, app.TranslationFilter{Locales: []string{"de"}}, firstPage())
			return err
		}, "not found"},
		{"scoped: write out of scope", func() error {
			_, _, err := h.svc.PutTranslation(scoped, p, "a", "de", app.TranslationInput{Text: "x"}, &revision)
			return err
		}, "not found"},
		{"scoped: locales out of scope", func() error { _, _, err := h.svc.ListLocales(scoped, p, firstPage()); return err }, "not found"},
		{"scoped: stats out of scope", func() error { _, err := h.svc.TranslationStats(scoped, p); return err }, "not found"},

		{"assigned: a covered unit", func() error { _, err := h.svc.GetTranslation(vendor, p, "a", "de"); return err }, "ok"},
		{"assigned: the covered message in another locale", func() error { _, err := h.svc.GetTranslation(vendor, p, "a", "fr"); return err }, "not found"},
		{"assigned: an uncovered message", func() error { _, err := h.svc.GetTranslation(vendor, p, "b", "de"); return err }, "not found"},
		{"assigned: a unit that does not exist", func() error { _, err := h.svc.GetTranslation(vendor, p, "zzz", "de"); return err }, "not found"},
		{"assigned: writes a covered unit", func() error {
			_, _, err := h.svc.PutTranslation(vendor, p, "a", "de", app.TranslationInput{Text: "Apfel"}, &revision)
			return err
		}, "ok"},
		{"assigned: writes another locale", func() error {
			_, _, err := h.svc.PutTranslation(vendor, p, "a", "fr", app.TranslationInput{Text: "Pomme"}, &revision)
			return err
		}, "not found"},
		{"assigned: writes an uncovered message", func() error {
			_, _, err := h.svc.PutTranslation(vendor, p, "b", "de", app.TranslationInput{Text: "Banane"}, &revision)
			return err
		}, "not found"},
		{"assigned: an uncovered unit's history", func() error {
			_, _, err := h.svc.TranslationRevisions(vendor, p, "a", "fr", firstPage())
			return err
		}, "not found"},
		{"assigned: reviews", func() error { _, err := h.svc.ReviewTranslation(vendor, p, "a", "de", "approved", 2); return err }, "denied"},
		{"assigned: imports", func() error {
			_, err := h.svc.ImportTranslations(vendor, p, []app.ImportItem{{Key: "a", Locale: "de", Text: "x"}})
			return err
		}, "denied"},
		{"assigned: stats", func() error { _, err := h.svc.TranslationStats(vendor, p); return err }, "denied"},
		{"assigned: the release snapshot", func() error { _, err := h.svc.ReleaseTranslations(vendor, p, nil); return err }, "denied"},
		{"assigned: the project's locales", func() error { _, _, err := h.svc.ListLocales(vendor, p, firstPage()); return err }, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcome(tc.call()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("lists", func(t *testing.T) {
		units := func(ctx context.Context) []string {
			rows, _, err := h.svc.ListProjectTranslations(ctx, p, app.TranslationFilter{Locales: []string{"de", "fr"}}, firstPage())
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, r := range rows {
				out = append(out, r.Key+"/"+r.Locale.String())
			}
			return out
		}
		if got := units(vendor); !slices.Equal(got, []string{"a/de"}) {
			t.Errorf("assigned project listing = %v, want only a/de", got)
		}
		if got := units(dev); len(got) != 3 {
			t.Errorf("unrestricted project listing = %v, want all three", got)
		}
		of, _, err := h.svc.ListTranslations(vendor, p, "a", firstPage())
		if err != nil || len(of) != 1 || of[0].Locale.String() != "de" {
			t.Errorf("assigned translations of a = %v, %v", of, err)
		}
	})
}
