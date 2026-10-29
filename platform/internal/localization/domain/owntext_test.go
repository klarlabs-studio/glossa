package domain_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

func TestAuthorCannotApproveOrRejectOwnText(t *testing.T) {
	for _, to := range []domain.ReviewState{domain.StateApproved, domain.StateRejected} {
		tr, _ := newTranslation(t, reviewing) // written by person:1
		if _, err := tr.Review(to, "person:1", reviewing.Flow, true, true, t0); !errors.Is(err, domain.ErrOwnText) {
			t.Errorf("author %s own text: %v", to, err)
		}
		if tr.State != domain.StateNeedsReview || tr.Revision != 1 {
			t.Errorf("refused review changed the translation: %+v", tr)
		}
		if _, err := tr.Review(to, "person:2", reviewing.Flow, true, true, t0); err != nil {
			t.Errorf("another reviewer %s: %v", to, err)
		}
	}
	// Sending one's own text back to draft is not a decision.
	tr, _ := newTranslation(t, reviewing)
	if _, err := tr.Review(domain.StateDraft, "person:1", reviewing.Flow, true, true, t0); err != nil {
		t.Errorf("author to draft: %v", err)
	}
	// Approving through a write of the same text is the same decision.
	tr, _ = newTranslation(t, reviewing)
	if _, _, err := tr.Revise(domain.Write{
		Content: tr.Content, Provenance: human(t), SourceRevision: 1, State: state(domain.StateApproved),
	}, reviewing, true, true, t0); !errors.Is(err, domain.ErrOwnText) {
		t.Errorf("author approves via write: %v", err)
	}
}

func TestImportedAndMachineTextStaysReviewable(t *testing.T) {
	for _, origin := range []domain.Origin{domain.OriginImport, domain.OriginAI, domain.OriginMachineTranslation} {
		by := "token:9"
		if origin == domain.OriginImport {
			by = "person:1"
		}
		prov, err := domain.NewProvenance(origin, nil, by)
		if err != nil {
			t.Fatal(err)
		}
		w := write(t, "Jetzt bezahlen", 1)
		w.Provenance = prov
		tr, _, err := domain.NewTranslation(uuid.New(), uuid.New(), de, w, reviewing, false, true, t0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Review(domain.StateApproved, "person:1", reviewing.Flow, true, true, t0); err != nil {
			t.Errorf("%s text approved by person:1: %v", origin, err)
		}
	}
}
