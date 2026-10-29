//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func lookup(t *testing.T, h *harness, q app.TMQuery) []app.TMMatch {
	t.Helper()
	ms, err := h.svc.LookupTM(h.translator(), q)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	return ms
}

func units(t *testing.T, h *harness, f app.UnitFilter) []domain.TMUnit {
	t.Helper()
	us, _, err := h.svc.ListUnits(h.translator(), f, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	return us
}

// An approved translation becomes a TM unit through the outbox, and a
// lookup finds it: exact (with its variables renamed to the query's),
// in context (101), and fuzzy.
func TestApprovedTranslationsBecomeTranslationMemory(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, map[string]string{
		"checkout.pay":   "Pay {amount, number} now",
		"checkout.total": "Total",
	})
	h.translate(t, p, "checkout.pay", "de", "Jetzt {amount, number} zahlen", nil) // approved on write
	h.translate(t, p, "checkout.total", "de", "Summe", ptr("draft"))
	h.drain(t)

	us := units(t, h, app.UnitFilter{})
	if len(us) != 1 {
		t.Fatalf("units = %d, want only the approved translation's", len(us))
	}
	u := us[0]
	if u.SourceNorm.Text != "Pay {1} now" || u.TargetMF2 != "Jetzt {$amount :number} zahlen" ||
		u.MessageKey != "checkout.pay" || u.Namespace != "default" || u.ProjectID == nil || *u.ProjectID != p ||
		u.Origin != domain.OriginTranslation || u.TranslationRevision != 1 {
		t.Errorf("unit = %+v", u)
	}

	base := app.TMQuery{ProjectID: &p, SourceLocale: tag("en"), TargetLocale: tag("de")}

	q := base
	q.Source = mf2(t, ".input {$total :number}\n{{Pay {$total} now}}")
	ms := lookup(t, h, q)
	if len(ms) != 1 || ms[0].Score != 100 || ms[0].Kind != domain.MatchExact || !ms[0].Adapted ||
		ms[0].TargetMF2 != "Jetzt {$total :number} zahlen" {
		t.Fatalf("exact = %+v", ms)
	}

	q.MessageKey, q.Namespace = "checkout.pay", "default"
	if ms := lookup(t, h, q); len(ms) != 1 || ms[0].Score != 101 || ms[0].Kind != domain.MatchContext {
		t.Errorf("context = %+v", ms)
	}

	q = base
	q.Source = mf1(t, "Pay {amount, number} now please")
	ms = lookup(t, h, q)
	if len(ms) != 1 || ms[0].Kind != domain.MatchFuzzy || ms[0].Score < 50 || ms[0].Score > 99 {
		t.Errorf("fuzzy = %+v", ms)
	}

	// A type change keeps the text but not the signature: fuzzy, 99.
	q.Source = mf1(t, "Pay {amount} now")
	if ms := lookup(t, h, q); len(ms) != 1 || ms[0].Score != 99 || ms[0].Kind != domain.MatchFuzzy {
		t.Errorf("signature change = %+v", ms)
	}

	// Asking for exact matches only leaves the fuzzy one out.
	q.MinScore = 100
	if ms := lookup(t, h, q); len(ms) != 0 {
		t.Errorf("min_score 100 = %+v", ms)
	}
	// Another locale pair sees nothing.
	q = base
	q.TargetLocale = tag("fr")
	q.Source = mf1(t, "Pay {amount, number} now")
	if ms := lookup(t, h, q); len(ms) != 0 {
		t.Errorf("fr = %+v", ms)
	}
}

// Matches come in the syntax asked for: MF1 when it can express the
// target, MF2 with the fallback flag when it can't. The MF2 target and
// model are there either way.
func TestTMTargetsInTheSyntaxAskedFor(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, map[string]string{"checkout.pay": "Pay {amount, number} now"})
	if _, err := h.catalog.UpsertMessages(h.developer(), catalogdomain.ProjectID(p), []catalogapp.UpsertItem{
		{Key: "legal.terms", Text: "Accept the {#b}terms{/b}", Syntax: "mf2"},
	}); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	h.translate(t, p, "checkout.pay", "de", "Jetzt {amount, number} zahlen", nil)
	h.translateMF2(t, p, "legal.terms", "de", "{#b}Bedingungen{/b} akzeptieren")
	h.drain(t)

	q := app.TMQuery{ProjectID: &p, SourceLocale: tag("en"), TargetLocale: tag("de"), MinScore: 100,
		Source: mf1(t, "Pay {total, number} now"), TargetSyntax: mfcontent.MF1}
	ms := lookup(t, h, q)
	if len(ms) != 1 || ms[0].TargetText != "Jetzt {total, number} zahlen" || ms[0].TargetSyntax != mfcontent.MF1 ||
		ms[0].SyntaxFallback || ms[0].TargetMF2 != "Jetzt {$total :number} zahlen" {
		t.Fatalf("mf1 = %+v", ms)
	}
	q.TargetSyntax = mfcontent.MF2
	if ms := lookup(t, h, q); len(ms) != 1 || ms[0].TargetText != "Jetzt {$total :number} zahlen" || ms[0].TargetSyntax != mfcontent.MF2 {
		t.Errorf("mf2 = %+v", ms)
	}
	q.TargetSyntax = ""
	if ms := lookup(t, h, q); len(ms) != 1 || ms[0].TargetSyntax != mfcontent.MF2 {
		t.Errorf("default for Go callers = %+v, want MF2", ms)
	}

	// Markup has no MF1 form.
	q.Source, q.TargetSyntax = mf2(t, "Accept the {#b}terms{/b}"), mfcontent.MF1
	ms = lookup(t, h, q)
	if len(ms) != 1 || ms[0].TargetSyntax != mfcontent.MF2 || !ms[0].SyntaxFallback ||
		ms[0].TargetText != "{#b}Bedingungen{/b} akzeptieren" {
		t.Errorf("fallback = %+v", ms)
	}
}

func TestTMUnitLifecycleKeepsHistory(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", true, []string{"de"}, map[string]string{"cart.empty": "Your cart is empty"})

	h.translate(t, p, "cart.empty", "de", "Dein Warenkorb ist leer", ptr("approved"))
	h.drain(t)
	if n := len(units(t, h, app.UnitFilter{})); n != 1 {
		t.Fatalf("active after approval = %d", n)
	}

	// Taking the approval back retires the unit.
	h.review(t, p, "cart.empty", "de", "needs_review")
	h.drain(t)
	if n := len(units(t, h, app.UnitFilter{})); n != 0 {
		t.Fatalf("active after unapproval = %d", n)
	}
	retired := units(t, h, app.UnitFilter{State: "retired"})
	if len(retired) != 1 || retired[0].RetiredReason != domain.RetireUnapproved || retired[0].RetiredBy == "" {
		t.Fatalf("retired = %+v", retired)
	}

	// Approving again derives a new unit; new approved text supersedes
	// it; new unapproved text overwrites that one.
	h.review(t, p, "cart.empty", "de", "approved")
	h.drain(t)
	h.translate(t, p, "cart.empty", "de", "Der Warenkorb ist leer", ptr("approved"))
	h.drain(t)
	h.translate(t, p, "cart.empty", "de", "Warenkorb leer", nil) // needs_review
	h.drain(t)

	all := units(t, h, app.UnitFilter{State: "all"})
	var reasons []domain.RetireReason
	for _, u := range all {
		reasons = append(reasons, u.RetiredReason)
	}
	want := []domain.RetireReason{domain.RetireUnapproved, domain.RetireSuperseded, domain.RetireOverwritten}
	if len(all) != 3 || reasons[0] != want[0] || reasons[1] != want[1] || reasons[2] != want[2] {
		t.Errorf("history reasons = %v, want %v", reasons, want)
	}
	if n := count(t, "SELECT count(*) FROM knowledge_tm_units WHERE retired_at IS NULL"); n != 0 {
		t.Errorf("active units = %d", n)
	}
}

// Deliveries are at least once and unordered: replaying every
// translation event, newest first, changes nothing.
func TestDerivationIsIdempotentAndOrderIndependent(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de", "fr"}, map[string]string{
		"a": "Save", "b": "Cancel", "c": "Delete",
	})
	h.translate(t, p, "a", "de", "Speichern", nil)
	h.drain(t)
	h.translate(t, p, "a", "de", "Sichern", nil)
	h.translate(t, p, "b", "de", "Abbrechen", nil)
	h.translate(t, p, "b", "fr", "Annuler", ptr("draft"))
	h.translate(t, p, "c", "fr", "Supprimer", nil)
	h.drain(t)
	snapshot := func() string {
		var s string
		if err := env.Super.QueryRow(context.Background(), `
			SELECT string_agg(target_mf2 || ':' || coalesce(retired_reason, 'active') || ':' || translation_revision, ',' ORDER BY target_mf2, created_at)
			FROM knowledge_tm_units`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	if before != "Abbrechen:active:1,Sichern:active:2,Speichern:superseded:1,Supprimer:active:1" {
		t.Fatalf("units = %s", before)
	}
	if _, err := env.Super.Exec(context.Background(), `
		UPDATE outbox_events
		SET status = 'pending', delivered_to = '{}', claim_token = NULL, delivered_at = NULL,
		    available_at = now() - (extract(epoch FROM occurred_at) * interval '1 millisecond')
		WHERE event_type LIKE 'localization.translation.%'`); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	if after := snapshot(); after != before {
		t.Errorf("replay changed the TM:\n%s\n%s", before, after)
	}
}

func TestLookupScopes(t *testing.T) {
	h := newHarness(t)
	shop := h.project(t, "shop", false, []string{"de"}, map[string]string{"save": "Save changes"})
	blog := h.project(t, "blog", false, []string{"de"}, map[string]string{"save": "Save changes"})
	h.translate(t, shop, "save", "de", "Änderungen speichern", nil)
	h.translate(t, blog, "save", "de", "Speichern", nil)
	h.drain(t)

	q := app.TMQuery{ProjectID: &shop, SourceLocale: tag("en"), TargetLocale: tag("de"), Source: mf1(t, "Save changes")}
	ms := lookup(t, h, q)
	if len(ms) != 1 || ms[0].TargetMF2 != "Änderungen speichern" {
		t.Errorf("project scope = %+v", ms)
	}
	q.AllProjects = true
	ms = lookup(t, h, q)
	if len(ms) != 2 || ms[0].TargetMF2 != "Änderungen speichern" || ms[1].TargetMF2 != "Speichern" {
		t.Errorf("tenant scope (own project first) = %+v", ms)
	}
	q.CountHits = true
	lookup(t, h, q)
	if n := count(t, "SELECT sum(hit_count) FROM knowledge_tm_units"); n != 2 {
		t.Errorf("hits = %d", n)
	}
}

func TestConcordanceAndManualRetirement(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, map[string]string{
		"a": "Your shopping cart is empty", "b": "Add to shopping cart", "c": "Checkout",
	})
	h.translate(t, p, "a", "de", "Dein Warenkorb ist leer", nil)
	h.translate(t, p, "b", "de", "In den Warenkorb", nil)
	h.translate(t, p, "c", "de", "Zur Kasse", nil)
	h.drain(t)

	ms, err := h.svc.Concordance(h.translator(), app.ConcordanceQuery{Query: "shopping cart", ProjectID: &p})
	if err != nil || len(ms) != 2 {
		t.Fatalf("source concordance = %+v, %v", ms, err)
	}
	ms, err = h.svc.Concordance(h.translator(), app.ConcordanceQuery{Query: "warenkorb", Side: domain.SideTarget, ProjectID: &p})
	if err != nil || len(ms) != 2 {
		t.Fatalf("target concordance = %+v, %v", ms, err)
	}
	if _, err := h.svc.Concordance(h.translator(), app.ConcordanceQuery{Query: "100%_"}); err != nil {
		t.Errorf("wildcards are literal: %v", err)
	}

	// Translators read; retiring by hand needs knowledge.write.
	id := ms[0].Unit.ID
	if err := h.svc.RetireUnit(h.translator(), id); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator retire: %v", err)
	}
	if err := h.svc.RetireUnit(h.developer(), id); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RetireUnit(h.developer(), id); err != nil {
		t.Errorf("retiring twice is a no-op: %v", err)
	}
	u, err := h.svc.GetUnit(h.translator(), id)
	if err != nil || u.Active() || u.RetiredReason != domain.RetireDeleted {
		t.Errorf("retired unit = %+v, %v", u, err)
	}
	if ms, _ := h.svc.Concordance(h.translator(), app.ConcordanceQuery{Query: "warenkorb", Side: domain.SideTarget, ProjectID: &p}); len(ms) != 1 {
		t.Errorf("a retired unit still matches: %+v", ms)
	}
}
