// Package po reads gettext PO files as a formats.Catalog (read only in
// M2, RFC 0003 §5).
//
// Mapping:
//
//   - msgctxt is the namespace, msgid the message ID and its source
//     text. gettext keys messages by source text, so the import job has
//     to derive message keys from it.
//   - msgid and msgstr are literal text: characters that are syntax in
//     MessageFormat stay literal, and printf directives (%s, %d) stay as
//     they are.
//   - #. (extracted comments, "comments for translators" in xgettext) is
//     the description; # (translator comments) is a note; #: are the
//     references. #| (previous msgid) and #~ (obsolete entries) are
//     skipped.
//   - The fuzzy flag makes the translation needs_review; other
//     translations take ReadOptions.TranslatedState (default approved:
//     a PO file without the flag says the translation is done). An empty
//     msgstr, or a plural with any empty form, is no translation.
//   - msgid_plural makes a plural: the source is an MF2 select on
//     $count (ReadOptions.PluralVariable) whose "one" variant is msgid
//     and whose catch-all variant is msgid_plural.
//   - The msgstr[i] of a plural map onto the target locale's CLDR plural
//     categories: the header's Plural-Forms expression is evaluated for
//     sample counts (0–1000 and powers of ten up to a million), and each
//     category takes the form its smallest sample selects; the catch-all
//     variant takes the form for "other" (or the last form, for languages
//     whose integers never select "other"). Categories come from the
//     MessageFormat kernel's CLDR data, so the result selects exactly as
//     runtimes do. Without Plural-Forms, the forms map in CLDR order
//     (zero, one, two, few, many, other) when their number equals the
//     locale's integer categories, which holds for gettext's standard
//     formulas; otherwise reading fails.
//   - The header's Language is the target locale (ReadOptions.Locale
//     overrides it); X-Source-Language, else ReadOptions.SourceLocale,
//     else English, is the source locale. A charset other than UTF-8 in
//     Content-Type is decoded.
package po

import (
	"io"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

const format = "po"

// ReadOptions configure Read.
type ReadOptions struct {
	// Locale of the translations; zero takes the header's Language.
	Locale bcp47.Tag
	// SourceLocale of the msgids; zero takes X-Source-Language, else en.
	SourceLocale bcp47.Tag
	// PluralVariable names the count variable of plurals ("count").
	PluralVariable string
	// TranslatedState is the state of translations without the fuzzy
	// flag ("" means approved).
	TranslatedState formats.State
	Limits          formats.Limits
}

// Read reads a PO file.
func Read(r io.Reader, opts ReadOptions) (formats.Catalog, error) {
	limits := opts.Limits.WithDefaults(formats.DefaultLimits)
	data, err := formats.ReadAll(r, limits.MaxBytes)
	if err != nil {
		return formats.Catalog{}, &formats.Error{Format: format, Err: err}
	}
	data, err = decode(data)
	if err != nil {
		return formats.Catalog{}, &formats.Error{Format: format, Err: err}
	}
	entries, err := parse(data, limits.MaxItems)
	if err != nil {
		return formats.Catalog{}, err
	}
	return convert(entries, opts)
}
