//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	"go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// reviewers answers "could anyone else review?" with a fixed answer and
// remembers who was excluded.
type reviewers struct {
	others   bool
	excluded string
}

func (r *reviewers) OthersCanReview(_ context.Context, _ uuid.UUID, _ bcp47.Tag, excluding string) (bool, error) {
	r.excluded = excluding
	return r.others, nil
}

// withReviewers is the harness's service with Identity's answer wired.
func (h *harness) withReviewers(r app.Reviewers) *app.Service {
	return app.New(postgres.NewTransactor(db.NewUnitOfWork(env.App)), catalogport.New(h.catalog), app.WithReviewers(r))
}

// RFC 0006 §15 Q6, amended 2026-10-08: the author rule applies only when
// someone else could review. Alone, the author decides and the log says so.
func TestAuthorReviewsOwnTextWhenNoOneElseCan(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de"}, map[string]string{"a": "One", "b": "Two"})
	author := h.as([]string{"translator", "reviewer"}, "de")
	solo := &reviewers{others: false}
	svc := h.withReviewers(solo)

	tr, _, err := svc.PutTranslation(author, p, "a", "de", app.TranslationInput{Text: "Eins"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ReviewByAuthorAllowed == nil || !*tr.ReviewByAuthorAllowed {
		t.Fatalf("the view should say the author may review: %+v", tr.ReviewByAuthorAllowed)
	}
	got, err := svc.ReviewTranslation(author, p, "a", "de", "approved", tr.Revision)
	if err != nil || got.State != domain.StateApproved {
		t.Fatalf("solo author approves: %+v %v", got, err)
	}
	if solo.excluded == "" {
		t.Error("the author was not excluded from the reviewers asked about")
	}
	revs, _, err := svc.TranslationRevisions(author, p, "a", "de", firstPage())
	if err != nil || len(revs) != 2 || !revs[0].SelfReview || revs[0].Kind != domain.KindReview || revs[1].SelfReview {
		t.Fatalf("history should mark only the review as a self-review: %+v %v", revs, err)
	}

	// Save & approve in one request: approved, marked as a self-review.
	w, _, err := svc.PutTranslation(author, p, "b", "de", app.TranslationInput{Text: "Zwei", State: ptr("approved")}, nil)
	if err != nil || w.State != domain.StateApproved {
		t.Fatalf("solo save & approve: %+v %v", w, err)
	}
	revs, _, err = svc.TranslationRevisions(author, p, "b", "de", firstPage())
	if err != nil || len(revs) != 1 || !revs[0].SelfReview {
		t.Fatalf("approving write should be a self-review: %+v %v", revs, err)
	}
}

// With another reviewer, the same author is refused, and a write asking
// for approval lands as needs_review (#90).
func TestAuthorCannotReviewOwnTextWhenAnotherCan(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, true, []string{"de"}, map[string]string{"a": "One", "b": "Two"})
	author := h.as([]string{"translator", "reviewer"}, "de")
	other := h.as([]string{"reviewer"}, "de")
	svc := h.withReviewers(&reviewers{others: true})

	tr, _, err := svc.PutTranslation(author, p, "a", "de", app.TranslationInput{Text: "Eins"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ReviewByAuthorAllowed == nil || *tr.ReviewByAuthorAllowed {
		t.Fatalf("the view should say another reviewer must: %+v", tr.ReviewByAuthorAllowed)
	}
	if _, err := svc.ReviewTranslation(author, p, "a", "de", "approved", tr.Revision); !errors.Is(err, domain.ErrOwnText) {
		t.Fatalf("author approves own text: %v", err)
	}
	if _, err := svc.ReviewTranslation(other, p, "a", "de", "approved", tr.Revision); err != nil {
		t.Fatalf("another reviewer approves: %v", err)
	}
	revs, _, err := svc.TranslationRevisions(other, p, "a", "de", firstPage())
	if err != nil || revs[0].SelfReview {
		t.Fatalf("a second reviewer's decision is no self-review: %+v %v", revs, err)
	}

	// Save & approve by the author lands as needs_review.
	w, _, err := svc.PutTranslation(author, p, "b", "de", app.TranslationInput{Text: "Zwei", State: ptr("approved")}, nil)
	if err != nil || w.State != domain.StateNeedsReview {
		t.Fatalf("save & approve with another reviewer: %+v %v", w, err)
	}
	// An import by the same person keeps what it asked for.
	if _, err := svc.ImportTranslations(author, p, []app.ImportItem{{Key: "b", Locale: "de", Text: "Zwei!", State: ptr("approved")}}); err != nil {
		t.Fatal(err)
	}
	cur, err := svc.GetTranslation(author, p, "b", "de")
	if err != nil || cur.State != domain.StateApproved || cur.Origin != domain.OriginImport {
		t.Fatalf("import keeps its state: %+v %v", cur, err)
	}
}
