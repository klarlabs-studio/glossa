package domain_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

func person() string { return "person:" + uuid.NewString() }

func newApproval(t *testing.T, n int, distinct bool) domain.Approval {
	t.Helper()
	a, err := domain.NewApproval(uuid.New(), domain.ApprovalSubject{Kind: domain.SubjectTranslation, ID: uuid.New(), Locale: "DE"},
		n, domain.RoleAssignee("reviewer"), distinct, nil, person(), t0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func grant(a *domain.Approval, who, author string) (bool, error) {
	return a.Decide(domain.Ballot{Principal: who, Verdict: domain.VerdictGranted, Eligible: true, Author: author, At: t0})
}

func TestNewApproval(t *testing.T) {
	a := newApproval(t, 2, true)
	if a.State != domain.ApprovalPending || a.Subject.Locale != "de" {
		t.Fatalf("a = %+v", a)
	}
	project, by := uuid.New(), person()
	subject := domain.ApprovalSubject{Kind: domain.SubjectTranslation, ID: uuid.New(), Locale: "de"}
	for name, fn := range map[string]func() error{
		"n of zero": func() error {
			_, err := domain.NewApproval(project, subject, 0, domain.RoleAssignee("reviewer"), true, nil, by, t0)
			return err
		},
		"n of eleven": func() error {
			_, err := domain.NewApproval(project, subject, 11, domain.RoleAssignee("reviewer"), true, nil, by, t0)
			return err
		},
		"a vendor": func() error {
			_, err := domain.NewApproval(project, subject, 1, domain.VendorAssignee(uuid.New()), true, nil, by, t0)
			return err
		},
		"a translation without a locale": func() error {
			_, err := domain.NewApproval(project, domain.ApprovalSubject{Kind: domain.SubjectTranslation, ID: uuid.New()}, 1, domain.RoleAssignee("reviewer"), true, nil, by, t0)
			return err
		},
		"a release request with a locale": func() error {
			_, err := domain.NewApproval(project, domain.ApprovalSubject{Kind: domain.SubjectReleaseRequest, ID: uuid.New(), Locale: "de"}, 1, domain.RoleAssignee("reviewer"), true, nil, by, t0)
			return err
		},
	} {
		if err := fn(); !errors.Is(err, domain.ErrInvalidApproval) {
			t.Errorf("%s: err = %v, want ErrInvalidApproval", name, err)
		}
	}
}

func TestApprovalNeedsNDistinctGranters(t *testing.T) {
	a := newApproval(t, 2, true)
	author, first, second := person(), person(), person()
	if ok, err := grant(&a, first, author); !ok || err != nil || a.State != domain.ApprovalPending {
		t.Fatalf("first grant: %v %v, state %s", ok, err, a.State)
	}
	// The same person granting again counts once and records nothing.
	if ok, err := grant(&a, first, author); ok || err != nil || len(a.Decisions) != 1 || a.State != domain.ApprovalPending {
		t.Fatalf("repeat grant: recorded %v, %v, %d decisions, state %s", ok, err, len(a.Decisions), a.State)
	}
	if ok, err := grant(&a, second, author); !ok || err != nil || a.State != domain.ApprovalGranted || a.ClosedAt == nil {
		t.Fatalf("second grant: %v %v, state %s", ok, err, a.State)
	}
	if got := a.Granters(author); len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("granters = %v", got)
	}
	if _, err := grant(&a, person(), author); !errors.Is(err, domain.ErrApprovalClosed) {
		t.Fatalf("a decision on a granted approval: err = %v", err)
	}
}

func TestOneDenialEndsIt(t *testing.T) {
	a := newApproval(t, 2, true)
	author := person()
	if _, err := grant(&a, person(), author); err != nil {
		t.Fatal(err)
	}
	ok, err := a.Decide(domain.Ballot{Principal: person(), Verdict: domain.VerdictDenied, Reason: "wrong term", Eligible: true, Author: author, At: t0})
	if !ok || err != nil || a.State != domain.ApprovalDenied {
		t.Fatalf("denial: %v %v, state %s", ok, err, a.State)
	}
	if last := a.Decisions[len(a.Decisions)-1]; last.Verdict != domain.VerdictDenied || last.Reason != "wrong term" {
		t.Fatalf("last decision = %+v", last)
	}
	if _, err := grant(&a, person(), author); !errors.Is(err, domain.ErrApprovalClosed) {
		t.Fatalf("a grant after a denial: err = %v", err)
	}
}

func TestFourEyes(t *testing.T) {
	author := person()
	t.Run("the author cannot decide", func(t *testing.T) {
		a := newApproval(t, 1, true)
		for _, v := range []domain.Verdict{domain.VerdictGranted, domain.VerdictDenied} {
			ok, err := a.Decide(domain.Ballot{Principal: author, Verdict: v, Eligible: true, Author: author, At: t0})
			if ok || !errors.Is(err, domain.ErrOwnText) {
				t.Fatalf("%s by the author: %v, %v", v, ok, err)
			}
		}
		if len(a.Decisions) != 0 || a.State != domain.ApprovalPending {
			t.Fatalf("a refused decision was recorded: %+v", a)
		}
	})
	t.Run("a grant stops counting when its granter becomes the author", func(t *testing.T) {
		a := newApproval(t, 2, true)
		reviewer := person()
		if _, err := grant(&a, reviewer, author); err != nil {
			t.Fatal(err)
		}
		// The reviewer rewrites the text; the next grant is counted
		// against them as its author.
		if _, err := grant(&a, person(), reviewer); err != nil {
			t.Fatal(err)
		}
		if a.State != domain.ApprovalPending || len(a.Granters(reviewer)) != 1 {
			t.Fatalf("state %s, granters %v: the author's own grant counted", a.State, a.Granters(reviewer))
		}
	})
	t.Run("without distinct_from_author the author counts", func(t *testing.T) {
		a := newApproval(t, 1, false)
		if _, err := grant(&a, author, author); err != nil || a.State != domain.ApprovalGranted {
			t.Fatalf("%v, state %s", err, a.State)
		}
	})
}

func TestOnlyPeopleDecide(t *testing.T) {
	a := newApproval(t, 1, true)
	for _, who := range []string{"token:" + uuid.NewString(), "system:" + uuid.NewString(), "person:", "unknown", ""} {
		if ok, err := grant(&a, who, person()); ok || !errors.Is(err, domain.ErrNotHuman) {
			t.Errorf("a grant by %q: %v, %v; want ErrNotHuman", who, ok, err)
		}
	}
	if ok, err := a.Decide(domain.Ballot{Principal: person(), Verdict: domain.VerdictGranted, Eligible: false, At: t0}); ok || !errors.Is(err, domain.ErrNotEligible) {
		t.Errorf("an ineligible grant: %v, %v", ok, err)
	}
	if len(a.Decisions) != 0 {
		t.Fatalf("refused decisions were recorded: %+v", a.Decisions)
	}
}
