//go:build integration

package app_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func TestImportTMUnitsAddsOnceAndLookupsFindThem(t *testing.T) {
	h := newHarness(t)
	shop := h.project(t, "shop", false, []string{"de"}, nil)
	ctx := h.developer()
	units := []domain.ImportedText{
		{SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, "Save changes"), Target: mf1(t, "Änderungen speichern")},
		{SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, "Save changes"), Target: mf1(t, "Änderungen speichern")},
		{SourceLocale: tag("en"), TargetLocale: tag("en"), Source: mf1(t, "a"), Target: mf1(t, "b")},
	}
	dry, err := h.svc.ImportTMUnits(ctx, &shop, units, true)
	if err != nil || dry[0].Status != app.ImportCreated || count(t, "SELECT count(*) FROM knowledge_tm_units") != 0 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	res, err := h.svc.ImportTMUnits(ctx, &shop, units, false)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != app.ImportCreated || res[1].Status != app.ImportUnchanged ||
		res[2].Status != app.ImportInvalid || res[2].Code != "invalid_unit" {
		t.Errorf("results = %+v", res)
	}
	again, err := h.svc.ImportTMUnits(ctx, &shop, units[:1], false)
	if err != nil || again[0].Status != app.ImportUnchanged {
		t.Errorf("re-import = %+v %v", again, err)
	}
	// Tenant-wide is another scope.
	if wide, err := h.svc.ImportTMUnits(ctx, nil, units[:1], false); err != nil || wide[0].Status != app.ImportCreated {
		t.Errorf("tenant-wide = %+v %v", wide, err)
	}
	ms := lookup(t, h, app.TMQuery{ProjectID: &shop, SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, "Save changes")})
	if len(ms) != 1 || ms[0].Score != domain.ScoreExact || ms[0].Unit.Origin != domain.OriginImport {
		t.Errorf("lookup = %+v", ms)
	}
	if _, err := h.svc.ImportTMUnits(h.translator(), &shop, units, false); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator imports TM: %v", err)
	}
}

func TestImportConceptsMergesAndOverwrites(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	id := uuid.Must(uuid.NewV7())
	invoice := domain.ConceptInput{Definition: "A bill", Terms: []domain.TermInput{
		{Locale: "en", Text: "invoice", Status: "preferred"}, {Locale: "de", Text: "Rechnung", Status: "preferred"},
	}}
	res, err := h.svc.ImportConcepts(ctx, nil, []app.ConceptImport{{ID: id, Input: invoice}}, false, false)
	if err != nil || res[0].Status != app.ImportCreated {
		t.Fatalf("create = %+v %v", res, err)
	}
	// Mark a term case-sensitive by hand; a re-import keeps it.
	c, err := h.svc.GetConcept(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	edited := invoice
	edited.Terms = append([]domain.TermInput(nil), invoice.Terms...)
	edited.Terms[0].CaseSensitive = true
	if _, err := h.svc.ReplaceConcept(ctx, id, edited, c.Version); err != nil {
		t.Fatal(err)
	}
	if res, err := h.svc.ImportConcepts(ctx, nil, []app.ConceptImport{{ID: id, Input: invoice}}, false, false); err != nil || res[0].Status != app.ImportUnchanged {
		t.Errorf("same content = %+v %v", res, err)
	}
	changed := invoice
	changed.Definition = "A request for payment"
	if res, err := h.svc.ImportConcepts(ctx, nil, []app.ConceptImport{{ID: id, Input: changed}}, false, false); err != nil ||
		res[0].Status != app.ImportConflict || res[0].Code != "concept_differs" {
		t.Errorf("merge = %+v %v", res, err)
	}
	if res, err := h.svc.ImportConcepts(ctx, nil, []app.ConceptImport{{ID: id, Input: changed}}, true, false); err != nil || res[0].Status != app.ImportUpdated {
		t.Errorf("overwrite = %+v %v", res, err)
	}
	c, _ = h.svc.GetConcept(ctx, id)
	if c.Definition != "A request for payment" || !c.Terms[0].CaseSensitive {
		t.Errorf("overwritten concept = %+v", c)
	}
	project := h.project(t, "shop", false, nil, nil)
	if res, err := h.svc.ImportConcepts(ctx, &project, []app.ConceptImport{{ID: id, Input: changed}}, true, false); err != nil ||
		res[0].Status != app.ImportInvalid || res[0].Code != "concept_scope" {
		t.Errorf("other scope = %+v %v", res, err)
	}
	bad := domain.ConceptInput{Terms: nil}
	if res, err := h.svc.ImportConcepts(ctx, nil, []app.ConceptImport{{ID: uuid.Must(uuid.NewV7()), Input: bad}}, false, false); err != nil ||
		res[0].Status != app.ImportInvalid || res[0].Code != "invalid_concept" {
		t.Errorf("invalid = %+v %v", res, err)
	}
	fresh := uuid.Must(uuid.NewV7())
	if res, err := h.svc.ImportConcepts(ctx, nil, []app.ConceptImport{{ID: fresh, Input: invoice}}, false, true); err != nil || res[0].Status != app.ImportCreated {
		t.Errorf("dry run = %+v %v", res, err)
	}
	if _, err := h.svc.GetConcept(ctx, fresh); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a dry run stored a concept: %v", err)
	}
}
