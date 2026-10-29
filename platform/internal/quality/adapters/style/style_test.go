package style_test

import (
	"testing"

	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/style"
)

// The line RFC 0005 §3.2 draws, drawn here: the mechanical fields cross
// into the style layer and the prose rules cannot, because the type on
// the other side has nowhere to put them.

func TestTheMechanicalFieldsCross(t *testing.T) {
	g := style.Mechanical(inteldomain.StyleGuide{
		Version: "tenant@3+de@1", Formality: "formal", Pronoun: "Sie",
		Punctuation: map[string]string{
			"quotes": "„“", "dash": "—", "ellipsis": "…",
			"space_before_unit": "true", "decimal_separator": ",", "grouping_separator": ".",
			"date_format": "dd.MM.yyyy",
		},
	})
	switch {
	case g.Version != "tenant@3+de@1":
		t.Errorf("version = %q, want the merged guide's", g.Version)
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

// The prose rules do not cross, and they cannot: layers.StyleGuide has
// no field for a rationale, so no rule can be graded from one.
func TestTheProseRulesDoNotCross(t *testing.T) {
	g := style.Mechanical(inteldomain.StyleGuide{
		Version: "tenant@3",
		Rules: []inteldomain.StyleRule{{
			ID: "r1", Rule: "Address the reader directly",
			Rationale: "Our brand is warm", Good: []string{"You can"}, Bad: []string{"One can"},
		}},
		Tone: []string{"warm", "concise"},
	})
	if g.Stated() {
		t.Errorf("a guide of nothing but prose states a mechanical rule: %+v", g)
	}
}

func TestAGuideThatStatesNothingMechanicalIsNotAGuideThisLayerHas(t *testing.T) {
	if style.Mechanical(inteldomain.StyleGuide{Version: "tenant@3"}).Stated() {
		t.Error("an empty guide states something")
	}
}

// A date format this cannot read states no order, and the layer grades
// nothing rather than guessing at one.
func TestAnUnreadableDateFormatStatesNoOrder(t *testing.T) {
	for _, format := range []string{"", "EEEE", "the long one", "MMMM"} {
		g := style.Mechanical(inteldomain.StyleGuide{Punctuation: map[string]string{"date_format": format}})
		if g.DateOrder != "" {
			t.Errorf("%q read as %q, want no order", format, g.DateOrder)
		}
	}
	for format, want := range map[string]string{
		"yyyy-MM-dd": "ymd", "dd/MM/yyyy": "dmy", "M/d/yy": "mdy", "d. MMMM yyyy": "dmy",
	} {
		if got := style.Mechanical(
			inteldomain.StyleGuide{Punctuation: map[string]string{"date_format": format}},
		).DateOrder; got != want {
			t.Errorf("%q read as %q, want %q", format, got, want)
		}
	}
}

func TestASingleQuotationCharacterIsBothHalves(t *testing.T) {
	g := style.Mechanical(inteldomain.StyleGuide{Punctuation: map[string]string{"quotes": "\""}})
	if g.QuoteOpen != `"` || g.QuoteClose != `"` {
		t.Errorf("quotes = %q…%q", g.QuoteOpen, g.QuoteClose)
	}
}
