// Package locale owns the Locale aggregate.
package locale

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"golang.org/x/text/language"
)

// ErrInvalidCode is returned when a code is not a usable BCP 47 language tag.
var ErrInvalidCode = errors.New("locale: code must be a BCP 47 language tag (language[-script][-region][-variant]), e.g. 'de', 'de-CH', 'zh-Hant-TW' or 'es-419'")

// ErrInvalidLabel is returned when a label is empty or oversize.
var ErrInvalidLabel = errors.New("locale: label must be 1-50 characters")

// MaxCodeLength is the RFC 5646 §4.4.1 minimum buffer for a tag,
// and the width of every locale column.
const MaxCodeLength = 35

// Code is a canonical BCP 47 language tag identifying a locale.
//
// Canonical means RFC 5646 §4.5 canonicalization: deprecated subtags
// replaced by their preferred values (iw → he) and scripts the
// language implies anyway suppressed (en-Latn-US → en-US). Casing
// follows the RFC conventions (en-US, zh-Hant).
//
// Extensions (-u-, -t-) and private use (-x-) are rejected: they
// carry formatting preferences, not a distinct body of translations,
// so they don't belong in a locale's identity.
type Code string

// NewCode parses, validates and canonicalizes a tag.
func NewCode(s string) (Code, error) {
	tag, err := language.BCP47.Parse(s)
	if err != nil {
		return "", ErrInvalidCode
	}
	if base, _, _ := tag.Raw(); base.String() == "und" {
		return "", ErrInvalidCode
	}
	if len(tag.Extensions()) > 0 {
		return "", ErrInvalidCode
	}
	canonical := tag.String()
	if len(canonical) > MaxCodeLength {
		return "", ErrInvalidCode
	}
	return Code(canonical), nil
}

// String returns the underlying code value.
func (c Code) String() string { return string(c) }

// tag re-parses the code. Rows stored before canonicalization
// existed may hold non-canonical forms, so this goes through the
// same canonicalizer as NewCode.
func (c Code) tag() language.Tag {
	tag, _ := language.BCP47.Parse(string(c))
	return tag
}

// Language returns the primary language subtag, e.g. "zh" for zh-Hant-TW.
func (c Code) Language() string {
	base, _, _ := c.tag().Raw()
	return base.String()
}

// Script returns the explicit script subtag ("Hant" for zh-Hant-TW),
// or "" when the tag has none. Use [Code.Direction] for rendering
// decisions; it also accounts for the script a language implies.
func (c Code) Script() string {
	_, script, _ := c.tag().Raw()
	if script == (language.Script{}) {
		return ""
	}
	return script.String()
}

// Region returns the explicit region subtag ("TW", "419"), or "" when
// the tag has none.
func (c Code) Region() string {
	_, _, region := c.tag().Raw()
	if region == (language.Region{}) {
		return ""
	}
	return region.String()
}

// Direction is the base text direction of a locale.
type Direction string

// Direction values, matching the HTML dir attribute.
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
// pa → Guru → ltr, pa-Arab → rtl). Unknown scripts default to LTR.
func (c Code) Direction() Direction {
	script, confidence := c.tag().Script()
	if confidence != language.No && rtlScripts[script.String()] {
		return RTL
	}
	return LTR
}

// Matches reports whether raw names the same locale as c, comparing
// canonical forms, so "HE", "iw" and "he" all match Code("he").
func (c Code) Matches(raw string) bool {
	want, err := NewCode(raw)
	if err != nil {
		return false
	}
	if have, err := NewCode(string(c)); err == nil {
		return have == want
	}
	return string(c) == raw
}

// Label is a validated display label (e.g. "Deutsch", "English (US)").
type Label string

// NewLabel parses and validates a label literal.
func NewLabel(s string) (Label, error) {
	if l := len(s); l == 0 || l > 50 {
		return "", ErrInvalidLabel
	}
	return Label(s), nil
}

// String returns the underlying label value.
func (l Label) String() string { return string(l) }

// Locale is a single (project, language) combination.
type Locale struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Code      Code
	Label     Label
	Enabled   bool
}

// Repository is the persistence port.
type Repository interface {
	Save(ctx context.Context, l Locale) error
	ListForProject(ctx context.Context, projectID uuid.UUID) ([]Locale, error)
	SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	Delete(ctx context.Context, id uuid.UUID) error
}
