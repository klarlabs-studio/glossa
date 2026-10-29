package domain_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// Nobody else could review: the author decides, and the revision says so.
func TestAuthorMayReviewOwnTextWhenNoOneElseCan(t *testing.T) {
	for _, to := range []domain.ReviewState{domain.StateApproved, domain.StateRejected} {
		tr, _ := newTranslation(t, reviewing)
		rev, err := tr.Review(to, "person:1", reviewing.Flow, true, false, t0)
		if err != nil || !rev.SelfReview || tr.State != to {
			t.Errorf("solo %s: %+v %v", to, rev, err)
		}
		// Another reviewer's decision is no self-review.
		tr, _ = newTranslation(t, reviewing)
		if rev, err = tr.Review(to, "person:2", reviewing.Flow, true, false, t0); err != nil || rev.SelfReview {
			t.Errorf("other reviewer %s: %+v %v", to, rev, err)
		}
	}
	// Still needs the permission.
	tr, _ := newTranslation(t, reviewing)
	if _, err := tr.Review(domain.StateApproved, "person:1", reviewing.Flow, false, false, t0); !errors.Is(err, domain.ErrReviewForbidden) {
		t.Errorf("solo without review permission: %v", err)
	}
}

func TestWriteAsApprovedIsASelfApproval(t *testing.T) {
	cases := []struct {
		name    string
		others  bool
		by      string
		origin  domain.Origin
		want    domain.ReviewState
		selfRev bool
	}{
		{"someone else could review", true, "person:1", domain.OriginHuman, domain.StateNeedsReview, false},
		{"no one else could", false, "person:1", domain.OriginHuman, domain.StateApproved, true},
		{"import is exempt", true, "person:1", domain.OriginImport, domain.StateApproved, false},
		{"token is exempt", true, "token:1", domain.OriginHuman, domain.StateApproved, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, err := domain.NewProvenance(tc.origin, nil, tc.by)
			if err != nil {
				t.Fatal(err)
			}
			w := write(t, "Jetzt bezahlen", 1)
			w.State, w.Provenance = state(domain.StateApproved), prov
			tr, rev, err := domain.NewTranslation(uuid.New(), uuid.New(), de, w, reviewing, true, tc.others, t0)
			if err != nil || tr.State != tc.want || rev.SelfReview != tc.selfRev {
				t.Fatalf("new: %s self=%v %v", tr.State, rev.SelfReview, err)
			}
			// Revising with new text is decided the same way.
			w.Content = write(t, "Jetzt kaufen", 1).Content
			rev, changed, err := tr.Revise(w, reviewing, true, tc.others, t0)
			if err != nil || !changed || tr.State != tc.want || rev.SelfReview != tc.selfRev {
				t.Fatalf("revise: %s self=%v %v", tr.State, rev.SelfReview, err)
			}
		})
	}
}
