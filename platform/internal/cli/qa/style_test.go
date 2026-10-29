package qa_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiclient"
	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

func ptr[T any](v T) *T { return &v }

// The line RFC 0005 §3.2 draws, drawn on the terminal's side too: the
// mechanical fields of the contract's effective guide cross into the
// guide the style layer grades, and the prose rules cannot.

func TestTheMechanicalFieldsCrossFromTheContract(t *testing.T) {
	g, ok := qa.StyleGuideOf(remote.EffectiveStyleGuide{
		Sources: []apiclient.StyleGuideSource{
			{StyleGuideId: "sg_tenant", Version: 3}, {StyleGuideId: "sg_de", Version: 1},
		},
		Fields: apiclient.StyleFields{
			Formality: &apiclient.StyleFormality{
				Register: ptr(apiclient.StyleFormalityRegister("formal")), Pronoun: ptr("Sie"),
			},
			Punctuation: &apiclient.StylePunctuation{
				Quotes: ptr("„“"), Dash: ptr(apiclient.StylePunctuationDash("—")),
				Ellipsis: ptr("…"), SpaceBeforeUnit: ptr(true),
			},
			Numbers: &apiclient.StyleNumbers{DecimalSeparator: ptr(","), GroupingSeparator: ptr(".")},
			Dates:   &apiclient.StyleDates{Format: ptr("dd.MM.yyyy")},
		},
	})
	if !ok {
		t.Fatal("a guide that states seven mechanical rules states none")
	}
	switch {
	case g.Version != "sg_tenant@3+sg_de@1":
		t.Errorf("version = %q, want every source merged, broadest first", g.Version)
	case g.Formality != "formal" || len(g.Pronouns) != 1 || g.Pronouns[0] != "Sie":
		t.Errorf("formality = %q %v", g.Formality, g.Pronouns)
	case g.QuoteOpen != "„" || g.QuoteClose != "“":
		t.Errorf("quotes = %q…%q, want the pair split", g.QuoteOpen, g.QuoteClose)
	case g.Dash != "—" || g.Ellipsis != "…":
		t.Errorf("dash = %q, ellipsis = %q", g.Dash, g.Ellipsis)
	case g.SpaceBeforeUnit == nil || !*g.SpaceBeforeUnit:
		t.Errorf("space before unit = %v", g.SpaceBeforeUnit)
	case g.Decimal != "," || g.Group != ".":
		t.Errorf("separators = %q %q", g.Decimal, g.Group)
	case g.DateOrder != "dmy":
		t.Errorf("date order = %q, want dmy read off dd.MM.yyyy", g.DateOrder)
	}
}

// A guide of nothing but prose is not a guide this layer has. ok is
// false, so the locale gets no entry in Project.Styles and the layer
// reads it as "nothing to check against" — which is what the server's
// own port answers for the same document.
func TestAGuideOfProseAloneStatesNothingMechanical(t *testing.T) {
	for _, g := range []remote.EffectiveStyleGuide{
		{},
		{Fields: apiclient.StyleFields{Tone: ptr([]string{"warm", "concise"})}},
		{Rules: []apiclient.StyleRule{{Id: "r1", Title: ptr("Address the reader directly"),
			Rationale: ptr("Our brand is warm")}}},
	} {
		if out, ok := qa.StyleGuideOf(g); ok {
			t.Errorf("%+v states a mechanical rule: %+v", g, out)
		}
	}
}

// An unreadable date format states no order, and a register the
// contract may grow that this does not know is not guessed at
// (intent §41).
func TestWhatCannotBeReadIsNotGuessedAt(t *testing.T) {
	g, _ := qa.StyleGuideOf(remote.EffectiveStyleGuide{Fields: apiclient.StyleFields{
		Formality:   &apiclient.StyleFormality{Register: ptr(apiclient.StyleFormalityRegister("brisk"))},
		Punctuation: &apiclient.StylePunctuation{Quotes: ptr(`"`)},
		Dates:       &apiclient.StyleDates{Format: ptr("EEEE")},
	}})
	if g.Formality != "" {
		t.Errorf("formality = %q, want none for a register this does not know", g.Formality)
	}
	if g.DateOrder != "" {
		t.Errorf("date order = %q, want none for a format this cannot read", g.DateOrder)
	}
	if g.QuoteOpen != `"` || g.QuoteClose != `"` {
		t.Errorf("quotes = %q…%q, want one character as both halves", g.QuoteOpen, g.QuoteClose)
	}
}
