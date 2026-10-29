package formats

import (
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

// State is a translation's review state in the exchange model. The values
// are the Localization context's review states; the import job maps them
// onto its own type.
type State string

// Review states.
const (
	StateDraft       State = "draft"
	StateNeedsReview State = "needs_review"
	StateApproved    State = "approved"
	StateRejected    State = "rejected"
)

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	switch s {
	case StateDraft, StateNeedsReview, StateApproved, StateRejected:
		return true
	}
	return false
}

// Catalog is a set of messages with their source and translations, the
// exchange shape of XLIFF, JSON and PO files.
type Catalog struct {
	// SourceLocale is the locale of every entry's Source.
	SourceLocale bcp47.Tag
	// TargetLocale is the locale the file's translations were read as
	// when the file holds one target locale (XLIFF's trgLang or the
	// reader's option, a JSON or PO file's locale); zero when there is
	// none (a source catalog).
	TargetLocale bcp47.Tag
	Entries      []Entry
}

// Entry is one message.
type Entry struct {
	// ID is the message key.
	ID string
	// Namespace groups messages into bundles; "" is the default.
	Namespace   string
	Description string
	// Notes are free-form notes for translators.
	Notes []string
	// References are source code locations ("src/app.ts:12").
	References []string
	// MaxLength bounds the translation in characters; 0 means none.
	MaxLength int
	// Source is the source-locale message. It is zero when a file
	// carries only translations (a JSON or PO file of one target locale
	// read without its source).
	Source mfcontent.Content
	// Targets holds at most one translation per locale, in file order.
	Targets []Target
	// Pos is where the entry is in the file it was read from (zero for
	// entries built in memory).
	Pos Position
}

// Target is a message's translation into one locale.
type Target struct {
	Locale  bcp47.Tag
	Content mfcontent.Content
	State   State
	// Pos is where the translation is in the file it was read from (the
	// XLIFF <target>, the PO msgstr); zero means its entry's position.
	Pos Position
}

// Position locates an item in the file a reader read it from, so an
// import can report every result where the file has it — not only the
// problem that fails a malformed file.
type Position struct {
	// Line and Column are 1-based; zero when unknown.
	Line   int
	Column int
	// Ref names the item in the format's own terms: an XLIFF fragment
	// identifier (#/f=checkout/u=pay), a JSON pointer (/checkout/pay), a
	// PO entry's msgctxt and msgid (msgctxt "menu" msgid "Open"), a TMX
	// <tu> or TBX concept entry by its place in the file (tu[12]).
	Ref string
}

// IsZero reports whether p is unknown.
func (p Position) IsZero() bool { return p == Position{} }

// Or returns p, or fallback when p is unknown.
func (p Position) Or(fallback Position) Position {
	if p.IsZero() {
		return fallback
	}
	return p
}

// Target returns e's translation into locale.
func (e Entry) Target(locale bcp47.Tag) (Target, bool) {
	for _, t := range e.Targets {
		if t.Locale == locale {
			return t, true
		}
	}
	return Target{}, false
}

// TMUnit is one translation memory unit: a source and a target text in
// two locales (RFC 0003 §2.1). Both texts are MessageFormat 2 messages.
type TMUnit struct {
	// ID is the unit's identifier in the exchange file (TMX tuid).
	ID           string
	SourceLocale bcp47.Tag
	TargetLocale bcp47.Tag
	Source       mfcontent.Content
	Target       mfcontent.Content
	// Props are typed properties, in file order (TMX <prop>).
	Props []Prop
	Notes []string
	// CreatedAt, ChangedAt and LastUsedAt are zero when unknown.
	CreatedAt  time.Time
	ChangedAt  time.Time
	LastUsedAt time.Time
	UsageCount int
	// Pos is where the unit is in the file: its target <tuv>, with the
	// <tu> as the reference.
	Pos Position
}

// Prop is a typed property of a TM unit.
type Prop struct {
	Type  string
	Value string
}

// Termbase is a set of concepts, the exchange shape of TBX files
// (RFC 0003 §2.2).
type Termbase struct {
	// Language is the document's language: the language of headers and
	// of notes that don't name their own.
	Language bcp47.Tag
	Concepts []Concept
}

// Concept is one terminological concept with its localized terms.
type Concept struct {
	ID string
	// Domain is the subject field.
	Domain      string
	Definitions []Definition
	Notes       []string
	Terms       []Term
	// Pos is where the concept entry is in the file.
	Pos Position
}

// Definition defines a concept, optionally in one locale.
type Definition struct {
	// Locale is zero for a definition that names no language.
	Locale bcp47.Tag
	Text   string
}

// Term is one designation of a concept in one locale.
type Term struct {
	Locale bcp47.Tag
	Text   string
	Status TermStatus
	// PartOfSpeech is a TBX-Basic value: noun, verb, adjective, adverb
	// or other ("" when unknown).
	PartOfSpeech string
	// Context is an example of the term in use.
	Context string
	// Notes are usage notes.
	Notes []string
}

// TermStatus says whether a term should be used.
type TermStatus string

// Term statuses (RFC 0003 §2.2).
const (
	TermPreferred  TermStatus = "preferred"
	TermAdmitted   TermStatus = "admitted"
	TermDeprecated TermStatus = "deprecated"
	TermForbidden  TermStatus = "forbidden"
)

// Valid reports whether s is a known status.
func (s TermStatus) Valid() bool {
	switch s {
	case TermPreferred, TermAdmitted, TermDeprecated, TermForbidden:
		return true
	}
	return false
}

// ParseLocale canonicalizes a language tag read from a file.
func ParseLocale(s string) (bcp47.Tag, error) {
	tag, err := bcp47.Parse(s)
	if err != nil {
		return bcp47.Tag{}, fmt.Errorf("locale %q: %w", s, err)
	}
	return tag, nil
}
