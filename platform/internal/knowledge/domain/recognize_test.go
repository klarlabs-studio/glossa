package domain_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// concept builds a concept from "locale:status:text" terms; a trailing
// "!" on the text makes the term case-sensitive.
func concept(t *testing.T, name string, terms ...string) domain.Concept {
	t.Helper()
	in := domain.ConceptInput{Definition: name}
	for _, spec := range terms {
		parts := strings.SplitN(spec, ":", 3)
		ti := domain.TermInput{Locale: parts[0], Status: parts[1], Text: parts[2]}
		if strings.HasSuffix(ti.Text, "!") {
			ti.Text, ti.CaseSensitive = strings.TrimSuffix(ti.Text, "!"), true
		}
		in.Terms = append(in.Terms, ti)
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
	c, err := domain.NewConcept(id, nil, in, "person:test", now)
	if err != nil {
		t.Fatalf("concept %s: %v", name, err)
	}
	return c
}

// hits renders recognition results as "text@start-end=concept".
func hits(tb *domain.Termbase, text, locale string) string {
	var out []string
	for _, h := range tb.Recognize(text, bcp47.MustParse(locale)) {
		c, _ := tb.Concept(h.ConceptID)
		out = append(out, fmt.Sprintf("%s@%d-%d=%s", h.Text, h.Start, h.End, c.Definition))
	}
	return strings.Join(out, " ")
}

func TestRecognize(t *testing.T) {
	tb := domain.NewTermbase([]domain.Concept{
		concept(t, "workspace", "en:preferred:workspace", "de:preferred:Arbeitsbereich", "de:forbidden:Workspace",
			"fr:preferred:espace de travail", "es:preferred:espacio de trabajo", "ja:preferred:ワークスペース"),
		concept(t, "account", "en:preferred:account", "de:preferred:Konto", "fr:preferred:compte", "es:preferred:cuenta",
			"ja:preferred:アカウント"),
		concept(t, "card", "en:preferred:card", "fr:preferred:carte"),
		concept(t, "credit card", "en:preferred:credit card", "fr:preferred:carte bancaire"),
		concept(t, "product", "en:preferred:Glossa!", "de:preferred:Glossa!", "ja:preferred:Glossa!"),
		concept(t, "invoice", "de:preferred:Rechnung"),
		concept(t, "art", "en:preferred:art"),
		concept(t, "meeting", "ja:preferred:会議"),
	})
	tests := []struct {
		name, locale, text, want string
	}{
		{"en: case folded", "en", "Open your Workspace", "Workspace@10-19=workspace"},
		{"en: plural inflection", "en", "Two workspaces", "workspaces@4-14=workspace"},
		{"en: prefix only at a word start", "en", "Start the party", ""},
		{"en: longest match wins", "en", "Add a credit card or a card", "credit card@6-17=credit card card@23-27=card"},
		{"en: case-sensitive product name", "en", "glossa or Glossa", "Glossa@10-16=product"},
		{"de: compound with hyphen", "de", "Neuer Arbeitsbereichs-Name", "Arbeitsbereichs@6-21=workspace"},
		{"de: plural", "de", "Offene Rechnungen", "Rechnungen@7-17=invoice"},
		{"de: compounds are not split", "de", "Rechnungsadresse", ""},
		{"de: forbidden terms are recognized too", "de", "Dein Workspace", "Workspace@5-14=workspace"},
		{"de: a de term applies to de-AT text", "de-AT", "Dein Konto", "Konto@5-10=account"},
		{"fr: multi-word, inflected", "fr", "Ajoutez vos cartes bancaires", "cartes bancaires@12-28=credit card"},
		{"fr: elision", "fr", "l'espace de travail", "espace de travail@2-19=workspace"},
		{"es: plural", "es", "Tus cuentas y espacios de trabajo", "cuentas@4-11=account espacios de trabajo@14-33=workspace"},
		{"es: a different word is no match", "es", "Un cuento", ""},
		{"ja: no spaces, substring", "ja", "ワークスペースを作成", "ワークスペース@0-21=workspace"},
		{"ja: several", "ja", "アカウントとワークスペースを削除", "アカウント@0-15=account ワークスペース@18-39=workspace"},
		{"ja: substring inside a longer word", "ja", "会議室を予約", "会議@0-6=meeting"},
		{"ja: latin terms between kana", "ja", "Glossaのアカウント", "Glossa@0-6=product アカウント@9-24=account"},
		{"placeholder object replacement separates words", "en", "Open ￼workspace", "workspace@8-17=workspace"},
		{"en: a short term needs an exact word", "en", "The arts", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hits(tb, tc.text, tc.locale); got != tc.want {
				t.Errorf("Recognize(%q, %s)\n got %q\nwant %q", tc.text, tc.locale, got, tc.want)
			}
		})
	}
}

func TestRecognizeKeepsHomonymsOnTheSameSpan(t *testing.T) {
	tb := domain.NewTermbase([]domain.Concept{
		concept(t, "bank account", "de:preferred:Konto"),
		concept(t, "user account", "de:admitted:Konto"),
	})
	got := hits(tb, "Kein Konto", "de")
	if got != "Konto@5-10=bank account Konto@5-10=user account" && got != "Konto@5-10=user account Konto@5-10=bank account" {
		t.Errorf("hits = %q", got)
	}
}

func TestRecognizeTurkishCaseFolding(t *testing.T) {
	tb := domain.NewTermbase([]domain.Concept{concept(t, "permission", "tr:preferred:izin")})
	if got := hits(tb, "İZİN verildi", "tr"); got != "İZİN@0-6=permission" {
		t.Errorf("hits = %q", got)
	}
}

func TestRecognizeIsDeterministic(t *testing.T) {
	cs := []domain.Concept{
		concept(t, "a", "en:preferred:alpha"), concept(t, "b", "en:preferred:beta"), concept(t, "c", "en:preferred:alpha beta"),
	}
	first := hits(domain.NewTermbase(cs), "alpha beta alpha", "en")
	for range 20 {
		if got := hits(domain.NewTermbase([]domain.Concept{cs[2], cs[0], cs[1]}), "alpha beta alpha", "en"); got != first {
			t.Fatalf("order-dependent: %q vs %q", got, first)
		}
	}
	if first != "alpha beta@0-10=c alpha@11-16=a" {
		t.Errorf("hits = %q", first)
	}
}

func TestNewConceptValidates(t *testing.T) {
	bad := []domain.ConceptInput{
		{},
		{Terms: []domain.TermInput{{Locale: "xx-invalid-!", Text: "a"}}},
		{Terms: []domain.TermInput{{Locale: "en", Text: "  "}}},
		{Terms: []domain.TermInput{{Locale: "en", Text: "..."}}},
		{Terms: []domain.TermInput{{Locale: "en", Text: "a", Status: "approved"}}},
		{Terms: []domain.TermInput{{Locale: "en", Text: "a", PartOfSpeech: "pronoun"}}},
		{Terms: []domain.TermInput{{Locale: "en", Text: "Card"}, {Locale: "en", Text: "card"}}},
	}
	for i, in := range bad {
		if _, err := domain.NewConcept(uuid.New(), nil, in, "x", now); err == nil {
			t.Errorf("input %d accepted", i)
		}
	}
}

func TestConceptReplaceKeepsTermIDsAndVersions(t *testing.T) {
	c := concept(t, "workspace", "en:preferred:workspace", "de:forbidden:Workspace")
	en := c.Terms[0].ID
	changed, err := c.Replace(domain.ConceptInput{Definition: "workspace", Terms: []domain.TermInput{
		{Locale: "en", Text: "workspace"}, {Locale: "de", Text: "Arbeitsbereich"},
	}}, "person:other", now)
	if err != nil || !changed {
		t.Fatalf("replace: %t, %v", changed, err)
	}
	if c.Version != 2 || c.Terms[0].ID != en || c.UpdatedBy != "person:other" {
		t.Errorf("concept = %+v", c)
	}
	changed, err = c.Replace(domain.ConceptInput{Definition: "workspace", Terms: []domain.TermInput{
		{Locale: "en", Text: "workspace"}, {Locale: "de", Text: "Arbeitsbereich"},
	}}, "person:third", now)
	if err != nil || changed || c.Version != 2 {
		t.Errorf("unchanged replace: %t, %v, version %d", changed, err, c.Version)
	}
}
