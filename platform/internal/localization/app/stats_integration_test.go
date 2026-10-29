//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

func TestTranslationStats(t *testing.T) {
	h, p := listing(t)
	if _, err := h.svc.ReviewTranslation(h.as([]string{"owner"}), p, "home.title", "fr", "rejected", 1); err != nil {
		t.Fatal(err)
	}
	ctx := h.as([]string{"translator"}, "de")

	stats, err := h.svc.TranslationStats(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	// legal.terms is obsolete: neither it nor its de translation counts.
	if stats.Messages != 4 || len(stats.Locales) != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	byCode := map[string]app.LocaleStats{}
	for _, l := range stats.Locales {
		byCode[l.Code.String()] = l
	}
	if en := byCode["en"]; !en.IsSource || en.Coverage != domain.SourceCoverage(4) {
		t.Errorf("en = %+v", en)
	}
	wantDE := domain.Coverage{Messages: 4, Translated: 2, Missing: 2, Outdated: 1,
		States: domain.StateCounts{Draft: 1, Approved: 1}}
	if de := byCode["de"]; de.IsSource || de.Coverage != wantDE {
		t.Errorf("de = %+v, want %+v", de.Coverage, wantDE)
	}
	wantFR := domain.Coverage{Messages: 4, Translated: 1, Missing: 3,
		States: domain.StateCounts{Approved: 1, Rejected: 1}}
	if fr := byCode["fr"]; fr.Coverage != wantFR {
		t.Errorf("fr = %+v, want %+v", fr.Coverage, wantFR)
	}
	if stats.Locales[0].Code.String() != "de" || stats.Locales[2].Code.String() != "fr" {
		t.Errorf("order = %v, %v, %v", stats.Locales[0].Code, stats.Locales[1].Code, stats.Locales[2].Code)
	}

	// A removed locale drops out; its translations are kept.
	if err := h.svc.RemoveLocale(h.developer(), p, "fr"); err != nil {
		t.Fatal(err)
	}
	if stats, err = h.svc.TranslationStats(ctx, p); err != nil || len(stats.Locales) != 2 {
		t.Errorf("after removing fr: %+v %v", stats, err)
	}

	if _, err := h.svc.TranslationStats(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	if _, err := h.svc.TranslationStats(context.Background(), p); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: %v", err)
	}
}

// A project Localization has only just heard of still reports its
// source locale.
func TestTranslationStatsOfANewProject(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, nil, nil)
	if _, err := env.Super.Exec(context.Background(), "DELETE FROM localization_locales WHERE project_id = $1", p); err != nil {
		t.Fatal(err)
	}
	stats, err := h.svc.TranslationStats(h.developer(), p)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Messages != 0 || len(stats.Locales) != 1 || !stats.Locales[0].IsSource || stats.Locales[0].Code.String() != "en" {
		t.Errorf("stats = %+v", stats)
	}
}
