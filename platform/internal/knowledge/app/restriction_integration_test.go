//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	kdomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound), errors.Is(err, app.ErrProjectNotFound):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestKnowledgeRestrictions: a project's style guides, concepts and TM
// are not found outside a principal's project scope, tenant-wide
// knowledge is changed only by someone limited to no project, and an
// assigned member reads the style guide and termbase for the locales of
// their units and nothing else — never TM search or concordance (RFC
// 0006 §3.3, §4.1).
func TestKnowledgeRestrictions(t *testing.T) {
	h := newHarness(t)
	dev := h.developer()
	a := h.project(t, "a", false, []string{"de", "fr"}, map[string]string{"pay": "Pay"})
	b := h.project(t, "b", false, []string{"de"}, map[string]string{"other": "Other"})
	terms := func(en, de string) kdomain.ConceptInput {
		return kdomain.ConceptInput{Terms: []kdomain.TermInput{{Locale: "en", Text: en}, {Locale: "de", Text: de}}}
	}
	if _, _, err := h.svc.CreateConcept(dev, nil, terms("checkout", "Kasse"), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateConcept(dev, &a, terms("pay", "Bezahlen"), ""); err != nil {
		t.Fatal(err)
	}
	bConcept, _, err := h.svc.CreateConcept(dev, &b, terms("other", "Andere"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{Locale: "de", StyleInput: kdomain.StyleInput{Name: "tenant"}}, ""); err != nil {
		t.Fatal(err)
	}
	bGuide, _, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{ProjectID: &b, StyleInput: kdomain.StyleInput{Name: "b"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := h.catalog.MessagesByKeys(dev, catalogdomain.ProjectID(a), []string{"pay"})
	if err != nil || len(msg) != 1 {
		t.Fatalf("message: %v, %v", msg, err)
	}

	scoped := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{a}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de", "fr")
	cov.Assign(member, a, msg["pay"].ID.UUID(), "de")
	de, fr := bcp47.MustParse("de"), bcp47.MustParse("fr")
	en := bcp47.MustParse("en")
	source := mf.Message{}

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: another project's concept", func() error { _, err := h.svc.GetConcept(scoped, bConcept.ID); return err }, "not found"},
		{"scoped: another project's guide", func() error { _, err := h.svc.GetStyleGuide(scoped, bGuide.ID); return err }, "not found"},
		{"scoped: another project's guide history", func() error {
			_, _, err := h.svc.StyleGuideVersions(scoped, bGuide.ID, firstPage())
			return err
		}, "not found"},
		{"scoped: replaces another project's concept", func() error {
			_, err := h.svc.ReplaceConcept(scoped, bConcept.ID, terms("x", "y"), bConcept.Version)
			return err
		}, "not found"},
		{"scoped: a concept for another project", func() error { _, _, err := h.svc.CreateConcept(scoped, &b, terms("x", "y"), ""); return err }, "not found"},
		{"scoped: a tenant-wide concept", func() error { _, _, err := h.svc.CreateConcept(scoped, nil, terms("x", "y"), ""); return err }, "denied"},
		{"scoped: its own project's concept", func() error { _, _, err := h.svc.CreateConcept(scoped, &a, terms("x", "y"), ""); return err }, "ok"},
		{"scoped: another project's effective style", func() error {
			_, err := h.svc.EffectiveStyle(scoped, app.StyleQuery{ProjectID: &b, Locale: de})
			return err
		}, "not found"},
		{"scoped: TM in another project", func() error {
			_, err := h.svc.LookupTM(scoped, app.TMQuery{ProjectID: &b, SourceLocale: en, TargetLocale: de, Source: source})
			return err
		}, "not found"},
		{"scoped: TM over every project", func() error {
			_, err := h.svc.LookupTM(scoped, app.TMQuery{ProjectID: &a, AllProjects: true, SourceLocale: en, TargetLocale: de, Source: source})
			return err
		}, "denied"},
		{"scoped: a list filtered to another project", func() error {
			_, _, err := h.svc.ListConcepts(scoped, app.ConceptFilter{ProjectID: &b}, firstPage())
			return err
		}, "not found"},

		{"assigned: style for a covered locale", func() error {
			_, err := h.svc.EffectiveStyle(vendor, app.StyleQuery{ProjectID: &a, Locale: de})
			return err
		}, "ok"},
		{"assigned: style for another locale", func() error {
			_, err := h.svc.EffectiveStyle(vendor, app.StyleQuery{ProjectID: &a, Locale: fr})
			return err
		}, "not found"},
		{"assigned: style for another project", func() error {
			_, err := h.svc.EffectiveStyle(vendor, app.StyleQuery{ProjectID: &b, Locale: de})
			return err
		}, "not found"},
		{"assigned: tenant-level style alone", func() error {
			_, err := h.svc.EffectiveStyle(vendor, app.StyleQuery{Locale: de})
			return err
		}, "denied"},
		{"assigned: terms for a covered locale", func() error {
			_, err := h.svc.RecognizeTerms(vendor, app.TermQuery{ProjectID: &a, Text: "pay", Locale: en, TargetLocale: &de})
			return err
		}, "ok"},
		{"assigned: terms for another locale", func() error {
			_, err := h.svc.RecognizeTerms(vendor, app.TermQuery{ProjectID: &a, Text: "pay", Locale: en, TargetLocale: &fr})
			return err
		}, "not found"},
		{"assigned: TM matches of a covered unit", func() error {
			r, err := h.svc.UnitTMMatches(vendor, app.UnitTMQuery{Project: a, Key: "pay", Locale: de})
			for _, m := range r.Matches {
				if m.Unit.ID != uuid.Nil || m.MessageKey != "" {
					return errors.New("a vendor's match names its unit")
				}
			}
			return err
		}, "ok"},
		{"assigned: TM matches of the message in another locale", func() error {
			_, err := h.svc.UnitTMMatches(vendor, app.UnitTMQuery{Project: a, Key: "pay", Locale: fr})
			return err
		}, "not found"},
		{"assigned: TM matches of an uncovered message", func() error {
			_, err := h.svc.UnitTMMatches(vendor, app.UnitTMQuery{Project: a, Key: "cancel", Locale: de})
			return err
		}, "not found"},
		{"assigned: TM matches in another project", func() error {
			_, err := h.svc.UnitTMMatches(vendor, app.UnitTMQuery{Project: b, Key: "other", Locale: de})
			return err
		}, "not found"},
		{"scoped: TM matches of a unit in another project", func() error {
			_, err := h.svc.UnitTMMatches(scoped, app.UnitTMQuery{Project: b, Key: "other", Locale: de})
			return err
		}, "not found"},
		{"scoped: TM matches of a unit in its project", func() error {
			_, err := h.svc.UnitTMMatches(scoped, app.UnitTMQuery{Project: a, Key: "pay", Locale: de})
			return err
		}, "ok"},
		{"assigned: TM lookup", func() error {
			_, err := h.svc.LookupTM(vendor, app.TMQuery{ProjectID: &a, SourceLocale: en, TargetLocale: de, Source: source})
			return err
		}, "denied"},
		{"assigned: concordance", func() error {
			_, err := h.svc.Concordance(vendor, app.ConcordanceQuery{Query: "pay", ProjectID: &a})
			return err
		}, "denied"},
		{"assigned: the termbase list", func() error { _, _, err := h.svc.ListConcepts(vendor, app.ConceptFilter{}, firstPage()); return err }, "denied"},
		{"assigned: the style guide list", func() error {
			_, _, err := h.svc.ListStyleGuides(vendor, app.StyleFilter{}, firstPage())
			return err
		}, "denied"},
		{"assigned: TM units", func() error { _, _, err := h.svc.ListUnits(vendor, app.UnitFilter{}, firstPage()); return err }, "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcome(tc.call()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("lists", func(t *testing.T) {
		cs, _, err := h.svc.ListConcepts(scoped, app.ConceptFilter{}, firstPage())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.ProjectID != nil && *c.ProjectID == b {
				t.Errorf("a project-scoped list holds another project's concept %s", c.ID)
			}
		}
		if len(cs) != 3 { // tenant-wide, a's, and the one the scoped member created
			t.Errorf("scoped concepts = %d, want 3", len(cs))
		}
		gs, _, err := h.svc.ListStyleGuides(scoped, app.StyleFilter{}, firstPage())
		if err != nil || len(gs) != 1 {
			t.Errorf("scoped guides = %d, %v; want only the tenant's", len(gs), err)
		}
		all, _, err := h.svc.ListStyleGuides(dev, app.StyleFilter{}, firstPage())
		if err != nil || len(all) != 2 {
			t.Errorf("unrestricted guides = %d, %v; want both", len(all), err)
		}
	})
}
