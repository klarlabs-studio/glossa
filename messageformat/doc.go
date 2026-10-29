// Package messageformat is Glossa's MessageFormat kernel for Go.
//
// The canonical representation of every message is the Unicode
// MessageFormat 2 data model (RFC 0002 §5): [Message] and its node types
// mirror testdata/unicode/data-model/message.schema.json, and their JSON
// encoding is exactly that schema's shape — the wire contract for
// precompiled messages in release artifacts.
//
// The package:
//
//   - parses MF2 syntax ([ParseMF2]) and ICU MessageFormat 1 ([ParseMF1])
//     into the model, validates it ([Validate]) and writes MF2 syntax
//     ([Stringify]). MF1 constructs map onto standard MF2 functions
//     wherever those reproduce the MF1 output (testdata/glossa/README.md);
//   - derives the metadata the platform needs: [Arguments] with their
//     types and selector cases, and [MarkupElements];
//   - checks that a translation is structurally compatible with its source
//     ([CheckCompat]), with stable finding codes;
//   - formats messages for the Go runtime with the required and draft MF2
//     functions, to a string ([Format]) or to formatted parts
//     ([FormatToParts]) that keep markup and placeholder boundaries.
//     [Decimal] and [Money] values format and select exactly, without
//     ever passing through a float, and [WithTimeZone] sets the zone of
//     the date and time placeholders that don't name one.
//
// Errors are *[Error] values with stable codes ([ErrorCode]); formatting
// problems come as a *[FormatError] next to a usable fallback string. No
// function panics on user input.
//
// Third-party engines sit behind this package's API ([Parser],
// [Formatter], [Engine]) so they can be replaced; the Unicode conformance
// suite and Glossa's own fixtures in testdata/ prove any replacement.
package messageformat
