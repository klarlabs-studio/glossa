// Package tmx reads and writes TMX 1.4b translation memories as
// formats.TMUnit values, streaming in both directions.
//
// Mapping:
//
//   - <tu> with two <tuv>s per unit: the source (the tuv whose xml:lang
//     is the tu's or the header's srclang) and the target. Reading a tu
//     with more variants yields one unit per non-source variant; with
//     srclang="*all*" the first variant is the source. Language tags are
//     canonical BCP 47 (xml:lang, or the legacy lang attribute).
//   - tuid, creationdate, changedate, lastusagedate (YYYYMMDDThhmmssZ)
//     and usagecount are the unit's ID, dates and usage count; <prop>
//     and <note> on the tu are its props and notes. Props and notes on a
//     tuv are not carried, except Glossa's syntax marker.
//   - <seg> content of a simple message: text, and each placeholder as a
//     native code holding its MF2 syntax: <ph> for expressions and
//     standalone markup, <bpt>/<ept> for paired markup, <it> for
//     unpaired markup. Codes are numbered by x (and i for pairs); target
//     codes reuse the numbers of matching source codes.
//   - A complex message (variants or declarations) is MF2 syntax text in
//     <seg>, marked by <prop type="x-glossa-syntax">mf2</prop> on its tuv.
//   - Codes from other tools whose native data is not MF2 (<bpt>&lt;b&gt;
//     </bpt>) become standalone markup named after the element, with the
//     native code and the element's attributes as options:
//     {#tmx:bpt i=1 native=|<b>| /}. Writing turns such markup back into
//     the same element, so foreign TMX survives a round trip. <hi> is
//     read through; the text inside <sub> joins its code's native data.
package tmx

import "go.klarlabs.de/glossa/platform/internal/integration/formats"

const (
	format = "tmx"
	// PropSyntax marks a tuv whose seg is MF2 syntax text.
	PropSyntax = "x-glossa-syntax"
	// foreignPrefix names markup that stands for a foreign native code.
	foreignPrefix = "tmx:"
	dateLayout    = "20060102T150405Z"
)

// DefaultLimits bound Read and Reader: TMX streams, so the byte limit is
// higher than for catalog formats.
var DefaultLimits = formats.Limits{MaxBytes: 2 << 30, MaxItems: 10_000_000}
