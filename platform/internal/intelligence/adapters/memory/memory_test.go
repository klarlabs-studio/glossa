package memory_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/memory"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

var (
	ctx   = context.Background()
	scope = domain.Scope{TenantID: "t1"}
	enDE  = domain.LocalePair{Source: "en", Target: "de"}
)

func knowledge() *memory.Knowledge {
	k := memory.NewKnowledge("t1")
	k.AddUnits(
		memory.TMUnit{ID: "u1", Pair: enDE, Key: "files.count", Source: "You have {$n} files.", Target: "Du hast {$n} Dateien."},
		memory.TMUnit{ID: "u2", Pair: enDE, Source: "You have {$count} new files.", Target: "Du hast {$count} neue Dateien."},
		memory.TMUnit{ID: "u3", Pair: domain.LocalePair{Source: "en", Target: "fr"}, Source: "You have {$n} files.", Target: "Vous avez {$n} fichiers."},
		memory.TMUnit{ID: "u4", Pair: enDE, Source: "Completely different", Target: "Ganz anders"},
	)
	k.AddConcepts(
		memory.Concept{ID: "c-file", Definition: "an uploaded document", Terms: []domain.Term{
			{ID: "t-file-en", Text: "file", Locale: "en", Status: domain.TermPreferred},
			{ID: "t-datei", Text: "Datei", Locale: "de", Status: domain.TermPreferred},
			{ID: "t-file-de", Text: "File", Locale: "de", Status: domain.TermForbidden},
			{ID: "t-fichier", Text: "fichier", Locale: "fr", Status: domain.TermPreferred},
		}},
		memory.Concept{ID: "c-ws", Terms: []domain.Term{
			{ID: "t-ws-en", Text: "workspace", Locale: "en", Status: domain.TermPreferred},
			{ID: "t-ws-ja", Text: "ワークスペース", Locale: "ja", Status: domain.TermPreferred},
		}},
	)
	return k
}

func TestLookupTM(t *testing.T) {
	k := knowledge()
	got, err := k.LookupTM(ctx, scope, domain.TMQuery{Pair: enDE, Source: "You have {$count} files.", Key: "files.count"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UnitID != "u1" || got[0].Score != 101 || got[1].UnitID != "u2" || got[1].Score >= 100 || got[1].Score < 50 {
		t.Fatalf("matches = %+v", got)
	}
	got, _ = k.LookupTM(ctx, scope, domain.TMQuery{Pair: enDE, Source: "You have {$x} files."})
	if got[0].Score != 100 {
		t.Errorf("exact without key context = %d, want 100", got[0].Score)
	}
	if _, err := k.LookupTM(ctx, domain.Scope{TenantID: "t2"}, domain.TMQuery{Pair: enDE}); !errors.Is(err, memory.ErrForeignTenant) {
		t.Errorf("another tenant's scope: %v", err)
	}
}

func TestTerminology(t *testing.T) {
	k := knowledge()
	hits, err := k.RecognizeTerms(ctx, scope, enDE, "Upload 3 Files to the workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ConceptID != "c-file" || len(hits[0].Targets) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	tests := []struct {
		name, source, translation string
		want                      []string
	}{
		{"preferred used, inflected", "Upload a file", "Lade eine Datei hoch, dann Dateien", nil},
		{"missing preferred", "Upload a file", "Lade ein Dokument hoch", []string{domain.FindingTermMissing}},
		{"forbidden used", "Upload a file", "Lade ein File hoch", []string{domain.FindingTermMissing, domain.FindingTermForbidden}},
		{"forbidden without source term", "Hello", "Ein File", []string{domain.FindingTermForbidden}},
		{"nothing recognized", "Hello", "Hallo", nil},
	}
	for _, tc := range tests {
		got, err := k.CheckTerminology(ctx, scope, enDE, tc.source, tc.translation)
		if err != nil {
			t.Fatal(err)
		}
		var codes []string
		for _, f := range got {
			codes = append(codes, f.Code)
		}
		if len(codes) != len(tc.want) {
			t.Errorf("%s: findings = %+v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range codes {
			if codes[i] != tc.want[i] {
				t.Errorf("%s: findings = %v, want %v", tc.name, codes, tc.want)
			}
		}
	}
	// Japanese has no spaces: substring match.
	f, _ := k.CheckTerminology(ctx, scope, domain.LocalePair{Source: "en", Target: "ja"}, "Open the workspace", "ワークスペースを開く")
	if len(f) != 0 {
		t.Errorf("ja findings = %+v", f)
	}
}

func TestStyleAndContext(t *testing.T) {
	k := knowledge()
	k.SetStyle("de", domain.StyleGuide{Version: "s1", Formality: domain.FormalityInformal, Pronoun: "du"})
	g, err := k.EffectiveStyle(ctx, scope, "de-AT", "")
	if err != nil || g.Version != "s1" {
		t.Errorf("style = %+v, %v", g, err)
	}
	k.SetContext(domain.MessageContext{MessageID: "m1", Description: "Button"})
	c, _ := k.MessageContext(ctx, scope, "m1", "de")
	if c.Description != "Button" {
		t.Errorf("context = %+v", c)
	}
	c, _ = k.MessageContext(ctx, scope, "unknown", "de")
	if c.MessageID != "unknown" {
		t.Errorf("unknown message context = %+v", c)
	}
}

func TestBudget(t *testing.T) {
	b := memory.NewBudget(map[string]domain.MicroUSD{"t1": 1000})
	if err := b.Check(ctx, scope, 800); err != nil {
		t.Fatal(err)
	}
	_ = b.Record(ctx, scope, domain.Spend{Cost: 600})
	if err := b.Check(ctx, scope, 500); !errors.Is(err, domain.ErrBudgetExceeded) {
		t.Errorf("over cap: %v", err)
	}
	if err := b.Check(ctx, domain.Scope{TenantID: "t2"}, 1); !errors.Is(err, domain.ErrBudgetExceeded) {
		t.Errorf("a tenant without a cap is refused: %v", err)
	}
	if b.Spent("t1") != 600 || len(b.Spends()) != 1 {
		t.Errorf("spent = %v", b.Spent("t1"))
	}
}
