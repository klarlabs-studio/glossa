//go:build integration

package app_test

import (
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// RFC 0006 §15 Q6: an author never approves their own work, on the
// direct review path as on the approvals path.
func TestAuthorCannotReviewOwnTranslation(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de"}, map[string]string{"a": "One", "b": "Two"})
	author := h.as([]string{"translator", "reviewer"}, "de")
	other := h.as([]string{"reviewer"}, "de")

	tr, _, err := h.svc.PutTranslation(author, p, "a", "de", app.TranslationInput{Text: "Eins"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"approved", "rejected"} {
		if _, err := h.svc.ReviewTranslation(author, p, "a", "de", state, tr.Revision); !errors.Is(err, domain.ErrOwnText) {
			t.Errorf("author %s own text: %v", state, err)
		}
	}
	still, err := h.svc.GetTranslation(author, p, "a", "de")
	if err != nil || still.State != domain.StateNeedsReview || still.Revision != tr.Revision {
		t.Fatalf("a refused review changed the translation: %+v %v", still, err)
	}
	// Approving through a write of the same text is the same decision.
	if _, _, err := h.svc.PutTranslation(author, p, "a", "de",
		app.TranslationInput{Text: "Eins", State: ptr("approved")}, ptr(tr.Revision)); !errors.Is(err, domain.ErrOwnText) {
		t.Errorf("author approves through a write: %v", err)
	}
	approved, err := h.svc.ReviewTranslation(other, p, "a", "de", "approved", tr.Revision)
	if err != nil || approved.State != domain.StateApproved {
		t.Fatalf("another member approves: %+v %v", approved, err)
	}
}

// An import is written by the importer, not authored by a person, so the
// person who ran it can still review what it brought in.
func TestImportedTranslationIsApprovable(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de"}, map[string]string{"a": "One"})
	importer := h.as([]string{"translator", "reviewer"}, "de")

	res, err := h.svc.ImportTranslations(importer, p, []app.ImportItem{{Key: "a", Locale: "de", Text: "Eins"}})
	if err != nil || len(res) != 1 || res[0].Error != nil {
		t.Fatalf("import: %+v %v", res, err)
	}
	cur, err := h.svc.GetTranslation(importer, p, "a", "de")
	if err != nil || cur.Origin != domain.OriginImport {
		t.Fatalf("imported translation: %+v %v", cur, err)
	}
	if got, err := h.svc.ReviewTranslation(importer, p, "a", "de", "approved", cur.Revision); err != nil || got.State != domain.StateApproved {
		t.Fatalf("approve an import: %+v %v", got, err)
	}
}
