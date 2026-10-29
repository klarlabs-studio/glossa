// Package bcp47 is the shared value object for a locale's identity: a
// canonical BCP 47 language tag (RFC 5646 §4.5) and the text direction
// it implies. Catalog (a project's source locale) and Localization (the
// locales a project translates into) both use it, and runtimes receive
// exactly this form in release manifests (runtimes/SPEC.md §1.1).
//
// Ported from v0.3's locale.Code, which proved the approach in
// production.
package bcp47

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// ErrInvalid means a string is not a usable locale tag.
var ErrInvalid = errors.New("bcp47: not a usable BCP 47 language tag (language[-script][-region][-variant], e.g. de, de-CH, zh-Hant-TW, es-419)")

// MaxLen is RFC 5646 §4.4.1's minimum buffer for a tag and the width of
// every locale column.
const MaxLen = 35

// Tag is a canonical BCP 47 language tag. The zero value is "no tag".
//
// Canonical means RFC 5646 §4.5: deprecated subtags replaced by their
// preferred values (iw → he), a script the language implies suppressed
// (en-Latn-US → en-US), conventional casing (en-US, zh-Hant). Extensions
// (-u-, -t-) and private use (-x-) are refused: they carry formatting
// preferences, not a distinct body of translations.
type Tag struct{ s string }

// Parse validates and canonicalizes s. Underscores are accepted as
// separators ("en_US") and surrounding space is trimmed.
func Parse(s string) (Tag, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 2*MaxLen {
		return Tag{}, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	tag, err := language.BCP47.Parse(s)
	if err != nil {
		return Tag{}, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	if base, _, _ := tag.Raw(); base.String() == "und" {
		return Tag{}, fmt.Errorf("%w: %q names no language", ErrInvalid, s)
	}
	if len(tag.Extensions()) > 0 {
		return Tag{}, fmt.Errorf("%w: %q: extensions and private use are not allowed", ErrInvalid, s)
	}
	canonical := tag.String()
	if len(canonical) > MaxLen {
		return Tag{}, fmt.Errorf("%w: %q is longer than %d characters", ErrInvalid, s, MaxLen)
	}
	return Tag{s: canonical}, nil
}

// MustParse is Parse for constants and tests; it panics on error.
func MustParse(s string) Tag {
	t, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return t
}

// String returns the canonical tag, "" for the zero value.
func (t Tag) String() string { return t.s }

// IsZero reports whether t is the zero value.
func (t Tag) IsZero() bool { return t.s == "" }

func (t Tag) tag() language.Tag {
	tag, _ := language.BCP47.Parse(t.s)
	return tag
}

// Direction is the base text direction of a locale, as in HTML's dir.
type Direction string

// Directions.
const (
	LTR Direction = "ltr"
	RTL Direction = "rtl"
)

// rtlScripts lists the ISO 15924 scripts whose characters are
// right-to-left (Unicode bidi class R or AL).
var rtlScripts = map[string]bool{
	"Adlm": true, "Arab": true, "Aran": true, "Armi": true, "Avst": true,
	"Chrs": true, "Cprt": true, "Elym": true, "Hatr": true, "Hebr": true,
	"Hung": true, "Khar": true, "Lydi": true, "Mand": true, "Mani": true,
	"Mend": true, "Merc": true, "Mero": true, "Narb": true, "Nbat": true,
	"Nkoo": true, "Orkh": true, "Ougr": true, "Palm": true, "Phli": true,
	"Phlp": true, "Phnx": true, "Prti": true, "Rohg": true, "Samr": true,
	"Sarb": true, "Sogd": true, "Sogo": true, "Syrc": true, "Thaa": true,
	"Yezi": true,
}

// Direction derives the text direction from the explicit script, or
// from the CLDR likely script when the tag has none (ar → Arab → rtl,
// pa → Guru → ltr, pa-Arab → rtl). An unknown script is LTR.
func (t Tag) Direction() Direction {
	if t.IsZero() {
		return LTR
	}
	script, confidence := t.tag().Script()
	if confidence != language.No && rtlScripts[script.String()] {
		return RTL
	}
	return LTR
}

// Truncations returns t's progressively shorter prefixes, as RFC 4647
// §3.4 Lookup truncates (zh-Hant-TW → zh-Hant → zh), dropping a trailing
// single-character subtag together with the one before it. They are
// what a runtime tries when a locale has no explicit fallback
// (runtimes/SPEC.md §4.2).
func (t Tag) Truncations() []Tag {
	parts := strings.Split(t.s, "-")
	var out []Tag
	for len(parts) > 1 {
		parts = parts[:len(parts)-1]
		if len(parts) > 1 && len(parts[len(parts)-1]) == 1 {
			parts = parts[:len(parts)-1]
		}
		out = append(out, Tag{s: strings.Join(parts, "-")})
	}
	return out
}
