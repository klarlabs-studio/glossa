//go:build integration

package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	intelligencedomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	qualitycatalog "go.klarlabs.de/glossa/platform/internal/quality/adapters/catalog"
	qualitypg "go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/sources"
)

// A delivered unit's numbers come from the real Catalog, Localization
// and Quality (RFC 0006 §3.4): the source's words, the text that stood
// at completion against the text that stands now, and the review state
// now — read as the caller, through each context's own use case.
func TestQualityFactsOfADeliveredUnit(t *testing.T) {
	h := newRunHarness(t)
	project, message := h.project(t) // source "Welcome", review required
	uow := db.NewUnitOfWork(env.App)
	quality := qualityapp.NewService(qualitypg.NewTransactor(uow), qualitycatalog.New(h.catalog))
	facts := sources.NewQualityFacts(h.catalog, h.localization, quality)

	translator, _ := h.as([]string{"translator"}, "de")
	reader, _ := h.as([]string{"translator"}, "de")
	before, err := facts.UnitQuality(reader, project, message, "de", time.Now())
	if err != nil || before.Found {
		t.Fatalf("a unit nobody translated: %+v, %v", before, err)
	}

	h.translate(t, translator, project, "Willkommen")
	time.Sleep(20 * time.Millisecond)
	completed := time.Now()
	time.Sleep(20 * time.Millisecond)

	got, err := facts.UnitQuality(reader, project, message, "de", completed)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || got.SourceWords != 1 || got.Changed || got.ReviewState != "needs_review" || got.EditDistance != 0 {
		t.Fatalf("as delivered: %+v", got)
	}

	// The reviewer rewrites the delivered text; a second reviewer approves
	// it (an author never approves their own text).
	reviewer, _ := h.as([]string{"reviewer"}, "de")
	edited := h.translate(t, reviewer, project, "Herzlich willkommen")
	approver, _ := h.as([]string{"reviewer"}, "de")
	if _, err := h.localization.ReviewTranslation(approver, project, "home.title", "de", "approved", edited.Revision); err != nil {
		t.Fatal(err)
	}
	got, err = facts.UnitQuality(reader, project, message, "de", completed)
	if err != nil {
		t.Fatal(err)
	}
	want := intelligencedomain.Levenshtein("Willkommen", "Herzlich willkommen")
	if !got.Found || !got.Changed || got.ReviewState != "approved" || got.EditDistance != want || got.EditRatio <= 0 {
		t.Fatalf("after the reviewer's edit: %+v, want an edit of %d", got, want)
	}

	// A message the project does not have is a unit whose facts are
	// unavailable, not an error that stops the report.
	gone, err := facts.UnitQuality(reader, project, uuid.New(), "de", completed)
	if err != nil || gone.Found {
		t.Errorf("an unknown message: %+v, %v", gone, err)
	}
}
