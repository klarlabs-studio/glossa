package domain_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

func binding(t *testing.T, project uuid.UUID, locales []string, ns string, pos int64) domain.Binding {
	t.Helper()
	b, err := domain.NewBinding(project, domain.SubjectTranslation, locales, ns, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	b.ID, b.Position = uuid.New(), pos
	return b
}

// TestBindingPrecedence is RFC 0006 §2.3's "check policy's precedence
// rule": the binding matching on more fields wins, ties go to the later
// one — resolved by checkpolicy.MostSpecific itself.
func TestBindingPrecedence(t *testing.T) {
	p := uuid.New()
	project := binding(t, p, nil, "", 1)
	german := binding(t, p, []string{"de", "de-AT"}, "", 2)
	checkout := binding(t, p, nil, "checkout", 3)
	germanCheckout := binding(t, p, []string{"de"}, "checkout", 4)
	laterProject := binding(t, p, nil, "", 5)
	all := []domain.Binding{project, german, checkout, germanCheckout, laterProject}

	cases := []struct {
		name   string
		target domain.Target
		want   *domain.Binding
	}{
		{"locale and namespace beat either", domain.Target{ProjectID: p, Locale: "de", Namespace: "checkout"}, &germanCheckout},
		{"a tie on specificity goes to the later binding",
			domain.Target{ProjectID: p, Locale: "fr", Namespace: "marketing"}, &laterProject},
		{"locale beats project", domain.Target{ProjectID: p, Locale: "de-AT", Namespace: "marketing"}, &german},
		{"namespace beats project", domain.Target{ProjectID: p, Locale: "fr", Namespace: "checkout"}, &checkout},
		{"locales are compared canonically", domain.Target{ProjectID: p, Locale: "de-at"}, &german},
		{"another project has no binding", domain.Target{ProjectID: uuid.New(), Locale: "de"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.target.Subject = domain.SubjectTranslation
			// Order in, order out must not matter: Position is "later".
			for _, in := range [][]domain.Binding{all, reversed(all)} {
				got, ok := domain.Resolve(in, tc.target)
				switch {
				case tc.want == nil && ok:
					t.Fatalf("resolved %+v, want none", got)
				case tc.want != nil && (!ok || got.ID != tc.want.ID):
					t.Fatalf("resolved position %d, want %d", got.Position, tc.want.Position)
				}
			}
		})
	}
}

func TestABindingSelectsOnlyItsSubject(t *testing.T) {
	p := uuid.New()
	b := binding(t, p, nil, "", 1)
	if _, ok := domain.Resolve([]domain.Binding{b}, domain.Target{ProjectID: p, Subject: domain.SubjectReleaseRequest}); ok {
		t.Error("a translation binding selected a release request")
	}
	// A release request has no locale, so a binding narrowed to locales
	// never applies to one.
	r, err := domain.NewBinding(p, domain.SubjectReleaseRequest, []string{"de"}, "", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if r.Matches(domain.Target{ProjectID: p, Subject: domain.SubjectReleaseRequest}) {
		t.Error("a locale-narrowed binding matched a target without a locale")
	}
}

func TestNewBindingValidates(t *testing.T) {
	p, d := uuid.New(), uuid.New()
	b, err := domain.NewBinding(p, domain.SubjectTranslation, []string{"fr-ca", "de", "de"}, "", d)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Locales, []string{"de", "fr-CA"}) || b.Specificity() != 2 {
		t.Errorf("locales = %v, specificity %d", b.Locales, b.Specificity())
	}
	for name, err := range map[string]error{
		"no project":    second(domain.NewBinding(uuid.Nil, domain.SubjectTranslation, nil, "", d)),
		"no definition": second(domain.NewBinding(p, domain.SubjectTranslation, nil, "", uuid.Nil)),
		"bad subject":   second(domain.NewBinding(p, "legal", nil, "", d)),
		"bad locale":    second(domain.NewBinding(p, domain.SubjectTranslation, []string{"not a locale"}, "", d)),
		"bad namespace": second(domain.NewBinding(p, domain.SubjectTranslation, nil, " padded ", d)),
	} {
		if !errors.Is(err, domain.ErrInvalidBinding) {
			t.Errorf("%s: err = %v, want ErrInvalidBinding", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

func reversed(in []domain.Binding) []domain.Binding {
	out := slices.Clone(in)
	slices.Reverse(out)
	return out
}
