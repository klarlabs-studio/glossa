package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func ptr[T any](v T) *T { return &v }

var project = uuid.MustParse("0192a1b2-0000-7000-8000-0000000000aa")

func guide(t *testing.T, name string, scope domain.StyleScope, in domain.StyleInput) domain.StyleGuide {
	t.Helper()
	in.Name = name
	g, err := domain.NewStyleGuide(uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)), scope, in, "person:test", now)
	if err != nil {
		t.Fatalf("guide %s: %v", name, err)
	}
	return g
}

func styleGuides(t *testing.T) []domain.StyleGuide {
	de := bcp47.MustParse("de")
	deAT := bcp47.MustParse("de-AT")
	return []domain.StyleGuide{
		guide(t, "tenant", domain.StyleScope{}, domain.StyleInput{Fields: domain.StyleFields{
			Formality:   domain.Formality{Register: ptr(domain.RegisterNeutral)},
			Tone:        []string{"concise", "friendly"},
			Punctuation: domain.Punctuation{SerialComma: ptr(true), Dash: ptr("en")},
		}, Rules: []domain.StyleRule{
			{ID: "no-exclamation", Title: "No exclamation marks", Rationale: "Calm product voice", Bad: []string{"Saved!"}, Good: []string{"Saved."}},
			{ID: "imperative-cta", Title: "Imperative CTAs", Rationale: "Direct"},
		}}),
		guide(t, "tenant-de", domain.StyleScope{Locale: &de}, domain.StyleInput{Fields: domain.StyleFields{
			Formality:   domain.Formality{Register: ptr(domain.RegisterFormal), Pronoun: ptr("Sie")},
			Punctuation: domain.Punctuation{Quotes: ptr("„“"), SerialComma: ptr(false)},
			Numbers:     domain.NumberStyle{DecimalSeparator: ptr(",")},
		}, Rules: []domain.StyleRule{{ID: "no-anglicisms", Title: "Avoid unnecessary anglicisms", Rationale: "Readers"}}}),
		guide(t, "project", domain.StyleScope{ProjectID: &project}, domain.StyleInput{Fields: domain.StyleFields{
			Formality: domain.Formality{Register: ptr(domain.RegisterInformal)},
			Tone:      []string{"playful"},
		}, Rules: []domain.StyleRule{{ID: "no-exclamation", Disabled: true}}}),
		guide(t, "project-de", domain.StyleScope{ProjectID: &project, Locale: &de}, domain.StyleInput{Fields: domain.StyleFields{
			Formality: domain.Formality{Register: ptr(domain.RegisterInformal), Pronoun: ptr("du")},
		}}),
		guide(t, "project-de-AT", domain.StyleScope{ProjectID: &project, Locale: &deAT}, domain.StyleInput{Fields: domain.StyleFields{
			Dates: domain.DateStyle{Format: ptr("d. MMMM y")},
		}}),
		guide(t, "project-legal", domain.StyleScope{ProjectID: &project, Namespace: "legal"}, domain.StyleInput{Fields: domain.StyleFields{
			Formality: domain.Formality{Register: ptr(domain.RegisterFormal), Pronoun: ptr("Sie")},
			Tone:      []string{"precise"},
		}, Rules: []domain.StyleRule{{ID: "imperative-cta", Title: "No imperatives in legal text", Rationale: "Liability"}}}),
		guide(t, "other-project", domain.StyleScope{ProjectID: ptr(uuid.New())}, domain.StyleInput{Fields: domain.StyleFields{
			Tone: []string{"never"},
		}}),
	}
}

func sources(e domain.EffectiveStyle, gs []domain.StyleGuide) string {
	names := map[uuid.UUID]string{}
	for _, g := range gs {
		names[g.ID] = g.Name
	}
	var out []string
	for _, s := range e.Sources {
		out = append(out, names[s.ID])
	}
	return strings.Join(out, " < ")
}

func ruleIDs(e domain.EffectiveStyle) string {
	var out []string
	for _, r := range e.Rules {
		out = append(out, r.ID+":"+r.Title)
	}
	return strings.Join(out, ", ")
}

func TestEffectiveStyleMergesFieldByFieldNarrowestWins(t *testing.T) {
	gs := styleGuides(t)
	tests := []struct {
		name      string
		project   *uuid.UUID
		locale    string
		namespace string
		sources   string
		register  domain.Register
		pronoun   string
		tone      string
		rules     string
	}{
		{"tenant, English", nil, "en", "", "tenant", domain.RegisterNeutral, "", "concise friendly",
			"no-exclamation:No exclamation marks, imperative-cta:Imperative CTAs"},
		{"tenant, German", nil, "de", "", "tenant < tenant-de", domain.RegisterFormal, "Sie", "concise friendly",
			"no-exclamation:No exclamation marks, imperative-cta:Imperative CTAs, no-anglicisms:Avoid unnecessary anglicisms"},
		// The tenant's German guide is narrower than the project's
		// all-locale guide: locale beats project.
		{"project, English", &project, "en", "", "tenant < project", domain.RegisterInformal, "", "playful",
			"imperative-cta:Imperative CTAs"},
		{"project, German", &project, "de", "", "tenant < project < tenant-de < project-de", domain.RegisterInformal, "du", "playful",
			"imperative-cta:Imperative CTAs, no-anglicisms:Avoid unnecessary anglicisms"},
		{"project, Austrian German", &project, "de-AT", "default",
			"tenant < project < tenant-de < project-de < project-de-AT", domain.RegisterInformal, "du", "playful",
			"imperative-cta:Imperative CTAs, no-anglicisms:Avoid unnecessary anglicisms"},
		{"project, German legal", &project, "de", "legal",
			"tenant < project < tenant-de < project-de < project-legal", domain.RegisterFormal, "Sie", "precise",
			"imperative-cta:No imperatives in legal text, no-anglicisms:Avoid unnecessary anglicisms"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := domain.EffectiveStyleOf(gs, tc.project, bcp47.MustParse(tc.locale), tc.namespace)
			if got := sources(e, gs); got != tc.sources {
				t.Errorf("sources = %q, want %q", got, tc.sources)
			}
			f := e.Fields
			if f.Formality.Register == nil || *f.Formality.Register != tc.register {
				t.Errorf("register = %v, want %s", f.Formality.Register, tc.register)
			}
			pronoun := ""
			if f.Formality.Pronoun != nil {
				pronoun = *f.Formality.Pronoun
			}
			if pronoun != tc.pronoun {
				t.Errorf("pronoun = %q, want %q", pronoun, tc.pronoun)
			}
			if got := strings.Join(f.Tone, " "); got != tc.tone {
				t.Errorf("tone = %q, want %q", got, tc.tone)
			}
			if got := ruleIDs(e); got != tc.rules {
				t.Errorf("rules = %q\nwant %q", got, tc.rules)
			}
		})
	}
	de := domain.EffectiveStyleOf(gs, &project, bcp47.MustParse("de-AT"), "")
	p := de.Fields.Punctuation
	if p.Quotes == nil || *p.Quotes != "„“" || p.SerialComma == nil || *p.SerialComma || p.Dash == nil || *p.Dash != "en" {
		t.Errorf("punctuation merges leaf by leaf: %+v", p)
	}
	if de.Fields.Dates.Format == nil || *de.Fields.Dates.Format != "d. MMMM y" || de.Fields.Numbers.DecimalSeparator == nil {
		t.Errorf("dates/numbers = %+v %+v", de.Fields.Dates, de.Fields.Numbers)
	}
	for _, s := range de.Sources {
		if s.Version != 1 {
			t.Errorf("source %s version = %d", s.ID, s.Version)
		}
	}
}

func TestStyleGuideValidation(t *testing.T) {
	de := bcp47.MustParse("de")
	bad := []struct {
		scope domain.StyleScope
		in    domain.StyleInput
		err   error
	}{
		{domain.StyleScope{Namespace: "legal"}, domain.StyleInput{}, domain.ErrNamespaceScope},
		{domain.StyleScope{ProjectID: &project, Namespace: "Legal!"}, domain.StyleInput{}, domain.ErrInvalidStyleGuide},
		{domain.StyleScope{Locale: &de}, domain.StyleInput{Fields: domain.StyleFields{Formality: domain.Formality{Register: ptr(domain.Register("polite"))}}}, domain.ErrInvalidStyleGuide},
		{domain.StyleScope{}, domain.StyleInput{Fields: domain.StyleFields{Punctuation: domain.Punctuation{Dash: ptr("long")}}}, domain.ErrInvalidStyleGuide},
		{domain.StyleScope{}, domain.StyleInput{Rules: []domain.StyleRule{{ID: "Bad Id", Title: "x"}}}, domain.ErrInvalidStyleRule},
		{domain.StyleScope{}, domain.StyleInput{Rules: []domain.StyleRule{{ID: "a", Title: "x"}, {ID: "a", Title: "y"}}}, domain.ErrInvalidStyleRule},
		{domain.StyleScope{}, domain.StyleInput{Rules: []domain.StyleRule{{ID: "a"}}}, domain.ErrInvalidStyleRule},
	}
	for i, tc := range bad {
		if _, err := domain.NewStyleGuide(uuid.New(), tc.scope, tc.in, "x", now); !errors.Is(err, tc.err) {
			t.Errorf("case %d: err = %v, want %v", i, err, tc.err)
		}
	}
	// A disabled rule needs no title: it only switches off a broader one.
	if _, err := domain.NewStyleGuide(uuid.New(), domain.StyleScope{}, domain.StyleInput{Rules: []domain.StyleRule{{ID: "a", Disabled: true}}}, "x", now); err != nil {
		t.Errorf("disabled rule: %v", err)
	}
}

func TestStyleGuideReplaceVersions(t *testing.T) {
	g := guide(t, "g", domain.StyleScope{}, domain.StyleInput{Fields: domain.StyleFields{Tone: []string{"calm"}}})
	changed, err := g.Replace(domain.StyleInput{Name: "g", Fields: domain.StyleFields{Tone: []string{"calm"}}}, "person:b", now)
	if err != nil || changed || g.Version != 1 {
		t.Errorf("unchanged: %t %v v%d", changed, err, g.Version)
	}
	changed, err = g.Replace(domain.StyleInput{Name: "g", Fields: domain.StyleFields{Tone: []string{"bold"}}}, "person:b", now)
	if err != nil || !changed || g.Version != 2 || g.UpdatedBy != "person:b" {
		t.Errorf("changed: %t %v %+v", changed, err, g)
	}
}
