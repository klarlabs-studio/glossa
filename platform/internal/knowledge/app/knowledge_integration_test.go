//go:build integration

package app_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	kdomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func workspace() kdomain.ConceptInput {
	return kdomain.ConceptInput{
		Definition: "Shared environment containing projects and members", Domain: "product",
		Terms: []kdomain.TermInput{
			{Locale: "en", Text: "workspace"},
			{Locale: "de", Text: "Arbeitsbereich", PartOfSpeech: "noun"},
			{Locale: "de", Text: "Workspace", Status: "forbidden"},
		},
	}
}

func TestTermbaseLifecycleRecognitionAndQA(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, nil)
	dev := h.developer()

	c, replayed, err := h.svc.CreateConcept(dev, nil, workspace(), "key-1")
	if err != nil || replayed || c.Version != 1 || len(c.Terms) != 3 {
		t.Fatalf("create = %+v, %t, %v", c, replayed, err)
	}
	again, replayed, err := h.svc.CreateConcept(dev, nil, workspace(), "key-1")
	if err != nil || !replayed || again.ID != c.ID {
		t.Errorf("replay = %+v, %t, %v", again, replayed, err)
	}
	if _, _, err := h.svc.CreateConcept(h.translator(), nil, workspace(), ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translators don't curate the termbase: %v", err)
	}
	unknown := uuid.New()
	if _, _, err := h.svc.CreateConcept(dev, &unknown, workspace(), ""); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	// A project-scoped concept only applies to that project.
	cart, _, err := h.svc.CreateConcept(dev, &p, kdomain.ConceptInput{Terms: []kdomain.TermInput{
		{Locale: "en", Text: "cart"}, {Locale: "de", Text: "Warenkorb"},
	}}, "")
	if err != nil {
		t.Fatal(err)
	}

	hits, err := h.svc.RecognizeTerms(h.translator(), app.TermQuery{
		ProjectID: &p, Text: "Add the cart to your workspace", Locale: tag("en"), TargetLocale: ptr(tag("de")),
	})
	if err != nil || len(hits) != 2 || hits[0].ConceptID != cart.ID || hits[1].Text != "workspace" ||
		len(hits[1].Targets) != 2 || hits[1].Targets[0].Text != "Arbeitsbereich" {
		t.Fatalf("recognized = %+v, %v", hits, err)
	}
	if hits, _ := h.svc.RecognizeTerms(h.translator(), app.TermQuery{Text: "your cart", Locale: tag("en")}); len(hits) != 0 {
		t.Errorf("a project concept outside its project: %+v", hits)
	}

	fs, err := h.svc.CheckTerminology(h.translator(), app.TermCheck{
		ProjectID: &p, Source: "Open your workspace", SourceLocale: tag("en"),
		Target: "Öffne deinen Workspace", TargetLocale: tag("de-AT"),
	})
	if err != nil || len(fs) != 2 || fs[0].Code != kdomain.FindingTermMissing || fs[1].Code != kdomain.FindingTermForbidden {
		t.Fatalf("findings = %+v, %v", fs, err)
	}

	// Replace (If-Match) versions the concept; history outlives delete.
	in := workspace()
	in.Terms = in.Terms[:2]
	if _, err := h.svc.ReplaceConcept(dev, c.ID, in, 7); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale If-Match: %v", err)
	}
	c2, err := h.svc.ReplaceConcept(dev, c.ID, in, 1)
	if err != nil || c2.Version != 2 || len(c2.Terms) != 2 || c2.Terms[0].ID != c.Terms[0].ID {
		t.Fatalf("replace = %+v, %v", c2, err)
	}
	search := "arbeits"
	listed, _, err := h.svc.ListConcepts(h.translator(), app.ConceptFilter{Query: &search}, firstPage())
	if err != nil || len(listed) != 1 || listed[0].ID != c.ID {
		t.Errorf("search = %+v, %v", listed, err)
	}
	if err := h.svc.DeleteConcept(dev, c.ID, ptr(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GetConcept(dev, c.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("deleted concept: %v", err)
	}
	revs, _, err := h.svc.ConceptRevisions(h.translator(), c.ID, firstPage())
	if err != nil || len(revs) != 3 || revs[0].Action != app.ActionDeleted || revs[2].Action != app.ActionCreated ||
		len(revs[2].Concept.Terms) != 3 {
		t.Errorf("history = %+v, %v", revs, err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type LIKE 'knowledge.concept.%'"); n != 4 {
		t.Errorf("concept events = %d", n)
	}
}

func TestStyleGuidesEffectiveViewAndVersions(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, nil)
	dev := h.developer()
	formal, informal := kdomain.RegisterFormal, kdomain.RegisterInformal

	tenantDE, _, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{Locale: "de", StyleInput: kdomain.StyleInput{
		Name: "German", Fields: kdomain.StyleFields{Formality: kdomain.Formality{Register: &formal, Pronoun: ptr("Sie")}},
		Rules: []kdomain.StyleRule{{ID: "no-anglicisms", Title: "Avoid anglicisms", Rationale: "Readers"}},
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{Locale: "de"}, ""); !errors.Is(err, app.ErrStyleGuideExists) {
		t.Errorf("second guide for a scope: %v", err)
	}
	projectDE, _, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{ProjectID: &p, Locale: "de", StyleInput: kdomain.StyleInput{
		Fields: kdomain.StyleFields{Formality: kdomain.Formality{Register: &informal, Pronoun: ptr("du")}, Tone: []string{"friendly"}},
	}}, "k")
	if err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{ProjectID: &p, Locale: "de"}, "k"); err != nil || !replayed {
		t.Errorf("replay: %t %v", replayed, err)
	}
	if _, _, err := h.svc.CreateStyleGuide(dev, app.NewStyleGuide{Namespace: "legal"}, ""); !errors.Is(err, kdomain.ErrNamespaceScope) {
		t.Errorf("namespace without project: %v", err)
	}

	e, err := h.svc.EffectiveStyle(h.translator(), app.StyleQuery{ProjectID: &p, Locale: tag("de-AT"), Namespace: "default"})
	if err != nil || len(e.Sources) != 2 || e.Sources[0].ID != tenantDE.ID || e.Sources[1].ID != projectDE.ID ||
		*e.Fields.Formality.Pronoun != "du" || len(e.Rules) != 1 {
		t.Fatalf("effective = %+v, %v", e, err)
	}
	if e, _ := h.svc.EffectiveStyle(h.translator(), app.StyleQuery{Locale: tag("de")}); len(e.Sources) != 1 || *e.Fields.Formality.Pronoun != "Sie" {
		t.Errorf("tenant-level effective = %+v", e)
	}

	g, err := h.svc.ReplaceStyleGuide(dev, projectDE.ID, kdomain.StyleInput{Fields: kdomain.StyleFields{Tone: []string{"playful"}}}, 1)
	if err != nil || g.Version != 2 {
		t.Fatalf("replace = %+v, %v", g, err)
	}
	e, _ = h.svc.EffectiveStyle(h.translator(), app.StyleQuery{ProjectID: &p, Locale: tag("de")})
	if e.Sources[1].Version != 2 || *e.Fields.Formality.Pronoun != "Sie" || e.Fields.Tone[0] != "playful" {
		t.Errorf("effective after replace = %+v", e)
	}
	if err := h.svc.DeleteStyleGuide(dev, projectDE.ID, nil); err != nil {
		t.Fatal(err)
	}
	vs, _, err := h.svc.StyleGuideVersions(h.translator(), projectDE.ID, firstPage())
	if err != nil || len(vs) != 3 || vs[0].Action != app.ActionDeleted || vs[1].Guide.Fields.Tone[0] != "playful" {
		t.Errorf("versions = %+v, %v", vs, err)
	}
	listed, _, err := h.svc.ListStyleGuides(h.translator(), app.StyleFilter{TenantOnly: true}, firstPage())
	if err != nil || len(listed) != 1 {
		t.Errorf("tenant guides = %+v, %v", listed, err)
	}
}

// Every Knowledge table is tenant-isolated: another tenant sees none of
// it, through any use case.
func TestCrossTenantIsolation(t *testing.T) {
	a := newHarness(t)
	p := a.project(t, "shop", false, []string{"de"}, map[string]string{"save": "Save changes"})
	a.translate(t, p, "save", "de", "Änderungen speichern", nil)
	a.drain(t)
	c, _, err := a.svc.CreateConcept(a.developer(), nil, workspace(), "")
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := a.svc.CreateStyleGuide(a.developer(), app.NewStyleGuide{Locale: "de"}, "")
	if err != nil {
		t.Fatal(err)
	}
	unit := units(t, a, app.UnitFilter{})[0]

	b := harnessFor(t, "globex")
	ctx := b.developer()
	if ms, err := b.svc.LookupTM(ctx, app.TMQuery{AllProjects: true, SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, "Save changes")}); err != nil || len(ms) != 0 {
		t.Errorf("lookup across tenants = %+v, %v", ms, err)
	}
	if ms, err := b.svc.Concordance(ctx, app.ConcordanceQuery{Query: "save"}); err != nil || len(ms) != 0 {
		t.Errorf("concordance across tenants = %+v, %v", ms, err)
	}
	if _, err := b.svc.GetUnit(ctx, unit.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unit across tenants: %v", err)
	}
	if err := b.svc.RetireUnit(ctx, unit.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("retire across tenants: %v", err)
	}
	if _, err := b.svc.GetConcept(ctx, c.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("concept across tenants: %v", err)
	}
	if hits, _ := b.svc.RecognizeTerms(ctx, app.TermQuery{Text: "workspace", Locale: tag("en")}); len(hits) != 0 {
		t.Errorf("recognition across tenants: %+v", hits)
	}
	if _, err := b.svc.GetStyleGuide(ctx, g.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("style guide across tenants: %v", err)
	}
	if e, _ := b.svc.EffectiveStyle(ctx, app.StyleQuery{Locale: tag("de")}); len(e.Sources) != 0 {
		t.Errorf("effective style across tenants: %+v", e)
	}
	// B's own guide for the same scope doesn't collide with A's.
	if _, _, err := b.svc.CreateStyleGuide(ctx, app.NewStyleGuide{Locale: "de"}, ""); err != nil {
		t.Errorf("same scope in another tenant: %v", err)
	}
}

func TestTokensAndRoles(t *testing.T) {
	h := newHarness(t)
	read := authztest.Token(h.developer(), h.tenant, "read")
	write := authztest.Token(h.developer(), h.tenant, "write")
	if _, _, err := h.svc.CreateConcept(read, nil, workspace(), ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("read token writes: %v", err)
	}
	if _, _, err := h.svc.CreateConcept(write, nil, workspace(), ""); err != nil {
		t.Errorf("write token (CI term import): %v", err)
	}
	if _, err := h.svc.RecognizeTerms(read, app.TermQuery{Text: "workspace", Locale: tag("en")}); err != nil {
		t.Errorf("read token reads: %v", err)
	}
	if _, err := h.svc.EffectiveStyle(h.as([]string{"reviewer"}, "de"), app.StyleQuery{Locale: tag("fr")}); err != nil {
		t.Errorf("a locale-scoped reviewer reads knowledge: %v", err)
	}
}

func TestDeletingAProjectErasesItsKnowledge(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, map[string]string{"save": "Save"})
	h.translate(t, p, "save", "de", "Speichern", nil)
	h.drain(t)
	if _, _, err := h.svc.CreateConcept(h.developer(), &p, workspace(), ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateStyleGuide(h.developer(), app.NewStyleGuide{ProjectID: &p}, ""); err != nil {
		t.Fatal(err)
	}
	tenantWide, _, err := h.svc.CreateConcept(h.developer(), nil, workspace(), "")
	if err != nil {
		t.Fatal(err)
	}
	owner := h.as([]string{"owner"})
	pr, err := h.catalog.GetProject(owner, domain.ProjectID(p))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.catalog.DeleteProject(owner, domain.ProjectID(p), &pr.Version); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	for _, table := range []string{"knowledge_tm_units", "knowledge_tm_derivations", "knowledge_style_guides",
		"knowledge_style_guide_versions"} {
		if n := count(t, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s keeps %d rows of a deleted project", table, n)
		}
	}
	if n := count(t, "SELECT count(*) FROM knowledge_concepts"); n != 1 {
		t.Errorf("concepts = %d, want only the tenant-wide one", n)
	}
	if _, err := h.svc.GetConcept(h.developer(), tenantWide.ID); err != nil {
		t.Errorf("tenant-wide concept: %v", err)
	}
}
