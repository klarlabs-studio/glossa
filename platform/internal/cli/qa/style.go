package qa

import (
	"strconv"

	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/style"
)

// The terminal's side of the effective style guide.
//
// The server resolves a locale's guide and reduces it to the mechanical
// fields the style layer grades; `glossa check` reads the same document
// over the API and has to arrive at the same reduction, or the terminal
// and the pull request would grade one translation against two guides.
//
// So the reduction is not repeated here. quality/adapters/style is where
// RFC 0005 §3.2's line is drawn — mechanical fields cross, prose rules
// cannot — and this file only puts the contract's shape into the shape
// that package already reads. The prose rules are dropped on the way in
// for the same reason they are dropped there: layers.StyleGuide has
// nowhere to put them, and "a regex over a rationale would be a lie
// about what the system knows."

// StyleGuideOf is locale's merged guide as the style layer grades it.
//
// ok is false where the guide states no mechanical rule at all, which
// the layer reads as "nothing to check against" rather than as a clean
// bill — the same answer, from the same predicate, that the server's
// own port gives (quality/adapters/style.Port.EffectiveStyle).
func StyleGuideOf(g remote.EffectiveStyleGuide) (StyleGuide, bool) {
	out := style.Mechanical(intel(g))
	return out, out.Stated()
}

// intel is the contract's effective guide in the shape
// quality/adapters/style reads: the typed leaves flattened onto the
// string map Knowledge stores them as, key for key with the server's own
// flattening (intelligence/adapters/sources.Knowledge.EffectiveStyle).
func intel(g remote.EffectiveStyleGuide) inteldomain.StyleGuide {
	out := inteldomain.StyleGuide{Version: styleVersion(g), Punctuation: map[string]string{}}
	if g.Fields.Tone != nil {
		out.Tone = *g.Fields.Tone
	}
	if f := g.Fields.Formality; f != nil {
		if f.Register != nil {
			switch string(*f.Register) {
			case inteldomain.FormalityFormal:
				out.Formality = inteldomain.FormalityFormal
			case inteldomain.FormalityInformal:
				out.Formality = inteldomain.FormalityInformal
			}
		}
		if f.Pronoun != nil {
			out.Pronoun = *f.Pronoun
		}
	}
	put := func(k string, v *string) {
		if v != nil {
			out.Punctuation[k] = *v
		}
	}
	putBool := func(k string, v *bool) {
		if v != nil {
			out.Punctuation[k] = strconv.FormatBool(*v)
		}
	}
	if p := g.Fields.Punctuation; p != nil {
		put("quotes", p.Quotes)
		put("nested_quotes", p.NestedQuotes)
		if p.Dash != nil {
			out.Punctuation["dash"] = string(*p.Dash)
		}
		put("ellipsis", p.Ellipsis)
		putBool("space_before_unit", p.SpaceBeforeUnit)
		putBool("space_before_punctuation", p.SpaceBeforePunctuation)
		putBool("serial_comma", p.SerialComma)
	}
	if n := g.Fields.Numbers; n != nil {
		put("decimal_separator", n.DecimalSeparator)
		put("grouping_separator", n.GroupingSeparator)
		put("number_notes", n.Notes)
	}
	if d := g.Fields.Dates; d != nil {
		put("date_format", d.Format)
		put("date_notes", d.Notes)
	}
	if len(out.Punctuation) == 0 {
		out.Punctuation = nil
	}
	return out
}

// styleVersion names every guide version merged, broadest first, the way
// the server names them — so the `style_guide` evidence on a finding
// from the terminal reads as the evidence on the same finding from the
// server.
func styleVersion(g remote.EffectiveStyleGuide) string {
	out := make([]byte, 0, len(g.Sources)*40)
	for i, s := range g.Sources {
		if i > 0 {
			out = append(out, '+')
		}
		out = append(out, s.StyleGuideId...)
		out = append(out, '@')
		out = strconv.AppendInt(out, int64(s.Version), 10)
	}
	return string(out)
}
