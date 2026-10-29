// Package xliff reads and writes XLIFF 2.1 (OASIS, core plus the Size and
// Length Restriction module) as a formats.Catalog.
//
// Mapping (one document carries one source and at most one target
// locale, as XLIFF requires):
//
//   - <xliff srcLang trgLang>: the catalog's source locale and the target
//     locale, canonical BCP 47. ReadOptions.TargetLocale names the
//     target locale instead: of a file without trgLang, or of one whose
//     trgLang the importer calls otherwise.
//   - <file original="namespace">: one file per namespace, in order of
//     first appearance. Nested <group>s are read through.
//   - <unit id name>: one unit per message. name is the message key; id
//     is the key too when it is an XML NMTOKEN, else a generated one.
//     Reading prefers name and falls back to id.
//   - <notes>: category="description" is the description,
//     category="location" a reference, any other note a translator note.
//   - slr:sizeRestriction on the unit (with the xliff:codepoints
//     profile declared on the file) is the max length.
//   - One <segment> per unit. Reading joins all segments and
//     ignorables, so a CAT tool may resegment.
//   - Review state, on <segment state subState>:
//     draft ↔ initial, needs_review ↔ translated, approved ↔ final
//     (reviewed reads as approved), rejected ↔ initial with
//     subState="glossa:rejected". A unit whose segments disagree takes the
//     least advanced state. Empty targets on segments without a state
//     are "not translated yet" (CAT tools write them); Glossa always
//     writes a state, so its empty translations survive.
//
// Message content has two representations:
//
//   - Simple messages (one pattern, no declarations) use native inline
//     codes, so any CAT tool can protect them: an expression or
//     standalone markup becomes <ph/>, properly nested open/close markup
//     becomes <pc>…</pc>, unpaired markup becomes isolated <sc/>/<ec/>.
//     Every code points (dataRef) at <originalData><data> holding its MF2
//     syntax ("{$name}", "{#b}"), and target codes reuse the IDs of the
//     matching source codes. Characters XML can't carry are written as
//     <cp hex/>.
//   - Complex messages (select/plural variants or declarations, in the
//     source or the target) are carried as MF2 syntax text, and the unit
//     is marked type="glossa:mf2". Translators edit the variants
//     directly, with the target locale's plural categories.
//
// Why not a full element mapping of MF2 select messages: XLIFF 2 has no
// standard representation of variants yet (the MessageFormat module is
// still a draft, and @messageformat/xliff's mapping is non-standard), a
// per-variant unit split would break TM leverage and length checks in
// CAT tools, and the target locale's variant keys differ from the
// source's (German one/* vs Polish one/few/many/*), which a mapping of
// source variants onto target segments cannot express. MF2 text keeps
// the structure exact and verifiable (the MessageFormat kernel parses it
// on import). The authoring syntax is not carried: an MF1 source comes
// back as the same canonical model, written in MF2.
//
// Units without Glossa's markers (from other tools) read their text
// literally, or as ICU MessageFormat with ReadOptions.PlainSyntax. Codes
// without original data (dataRef) are refused: their meaning is unknown.
package xliff

// Namespaces and Glossa's marker values.
const (
	// Namespace is the XLIFF 2 core namespace.
	Namespace = "urn:oasis:names:tc:xliff:document:2.0"
	// NamespaceSLR is the Size and Length Restriction module namespace.
	NamespaceSLR = "urn:oasis:names:tc:xliff:sizerestriction:2.0"
	// Version is the XLIFF version written.
	Version = "2.1"
	// TypeMF2 marks a unit whose source and target are MF2 syntax text.
	TypeMF2 = "glossa:mf2"
	// SubStateRejected marks a rejected translation.
	SubStateRejected = "glossa:rejected"

	format = "xliff"
)
