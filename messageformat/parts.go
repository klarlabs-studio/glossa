package messageformat

import "strings"

// Formatted parts (MF2 "formatted parts"): the output of [FormatToParts],
// for renderers that need the structure a flat string loses — markup
// boundaries, placeholder values and bidi isolation. The JSON encoding
// follows the Unicode conformance suite's expParts shape, with extra
// fields where Go has them (every part carries its text in "value").

// PartType names the kind of a formatted part.
type PartType string

// Part types. Placeholders resolve to string, number, datetime or unknown
// parts (the value's formatter decides); a placeholder that failed is a
// fallback part.
const (
	PartText          PartType = "text"          // literal text of the pattern
	PartMarkup        PartType = "markup"        // {#name}, {/name} or {#name/}
	PartBidiIsolation PartType = "bidiIsolation" // an isolation character around a placeholder
	PartFallback      PartType = "fallback"      // a placeholder that failed, as {source}
	PartString        PartType = "string"
	PartNumber        PartType = "number"
	PartDateTime      PartType = "datetime"
	PartUnknown       PartType = "unknown"
)

// Part is one formatted part of a message.
type Part struct {
	Type PartType `json:"type"`
	// Value is the part's text: the literal text, the isolation character,
	// a placeholder's formatted string, or a fallback's "{source}". It is
	// empty for markup. Joining the values ([PartsText]) gives exactly
	// what [Format] returns.
	Value string `json:"value,omitempty"`
	// Source identifies the placeholder a value or fallback came from
	// ("$count", "|literal|").
	Source string `json:"source,omitempty"`
	// Locale is the locale a placeholder was formatted with.
	Locale string `json:"locale,omitempty"`
	// Dir is a placeholder's direction when it is "ltr" or "rtl".
	Dir string `json:"dir,omitempty"`
	// ID is the u:id option of a placeholder or markup.
	ID string `json:"id,omitempty"`
	// Kind, Name and Options describe markup. Options are resolved: literals
	// as strings, variables as their argument values.
	Kind    MarkupKind     `json:"kind,omitempty"`
	Name    string         `json:"name,omitempty"`
	Options map[string]any `json:"options,omitempty"`
	// Parts are the sub-parts of a number or date/time (integer, group,
	// decimal, currency, year, literal, …), as Intl formatToParts reports
	// them.
	Parts []SubPart `json:"parts,omitempty"`
}

// SubPart is one piece of a formatted number or date/time.
type SubPart struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// PartsText joins the text of parts the way [Format] does: markup
// renders as nothing.
func PartsText(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Value)
	}
	return b.String()
}
