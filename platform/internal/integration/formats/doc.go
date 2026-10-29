// Package formats is the interchange layer of the Integration context
// (RFC 0003 §5): pure converters between localization exchange formats
// and a neutral in-memory exchange model. There is no I/O beyond the
// io.Reader/io.Writer a caller hands in, no database and no HTTP; the
// import/export jobs that use these converters live in integration/app.
//
// Formats are import/export formats, never the model (AGENTS.md):
// every message crosses this boundary as the canonical MessageFormat 2
// data model wrapped in the shared kernel's [mfcontent.Content], and every
// locale as a canonical [bcp47.Tag]. ICU MessageFormat 1 is converted by
// the MessageFormat kernel's one MF1 converter (messageformat.ParseMF1,
// RFC 0002 §5) and nowhere else.
//
// The exchange model:
//
//   - [Catalog]: messages ([Entry]) with an ID, namespace, description,
//     notes, references, max length, source content and one [Target]
//     per locale (content plus a review [State]). XLIFF, JSON and PO
//     read and write it.
//   - [TMUnit]: a translation memory unit (source and target locale and
//     content, props, notes, usage). TMX reads and writes it.
//   - [Termbase]: concepts with definitions and localized terms with a
//     [TermStatus]. TBX-Basic reads and writes it.
//
// Subpackages, one per format:
//
//   - xliff: XLIFF 2.1 (core + Size and Length Restriction module)
//   - tmx: TMX 1.4b, with a streaming reader
//   - tbx: TBX-Basic (TBX v3 DCA; TBX 2008 "martif" is read too)
//   - jsoncat: flat and nested {key: message} JSON catalogs
//   - po: gettext PO (read only in M2)
//
// Every reader is strict: malformed input returns an [*Error] with the
// format, line and column where known, and the item it was reading.
// Readers also record where each entry, translation, unit and concept
// is ([Position]: line, column and the format's own reference — an
// XLIFF fragment identifier, a JSON pointer, a PO msgctxt and msgid),
// so an import reports every result where the file has it.
// Readers bound their input ([Limits]); XML readers never process a
// document type definition or any entity beyond the five predefined
// ones, so external entities (XXE) and entity expansion bombs are
// refused, not resolved.
package formats
