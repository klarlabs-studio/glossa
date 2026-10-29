package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// TermStatus is a term's standing for its concept in its locale.
type TermStatus string

// Term statuses (RFC 0003 §2.2).
const (
	TermPreferred  TermStatus = "preferred"
	TermAdmitted   TermStatus = "admitted"
	TermDeprecated TermStatus = "deprecated"
	TermForbidden  TermStatus = "forbidden"
)

// Allowed reports whether translations may use a term with this status.
func (s TermStatus) Allowed() bool { return s == TermPreferred || s == TermAdmitted }

// ParseTermStatus validates s; "" means preferred.
func ParseTermStatus(s string) (TermStatus, error) {
	switch TermStatus(s) {
	case "":
		return TermPreferred, nil
	case TermPreferred, TermAdmitted, TermDeprecated, TermForbidden:
		return TermStatus(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidTermStatus, s)
}

// PartOfSpeech is a term's grammatical category (TBX-Basic).
type PartOfSpeech string

// Parts of speech; "" is unspecified.
const (
	PosNoun       PartOfSpeech = "noun"
	PosVerb       PartOfSpeech = "verb"
	PosAdjective  PartOfSpeech = "adjective"
	PosAdverb     PartOfSpeech = "adverb"
	PosProperNoun PartOfSpeech = "proper_noun"
	PosPhrase     PartOfSpeech = "phrase"
	PosOther      PartOfSpeech = "other"
)

// ParsePartOfSpeech validates s ("" is unspecified).
func ParsePartOfSpeech(s string) (PartOfSpeech, error) {
	switch PartOfSpeech(s) {
	case "", PosNoun, PosVerb, PosAdjective, PosAdverb, PosProperNoun, PosPhrase, PosOther:
		return PartOfSpeech(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidPartOfSpeech, s)
}

// Limits of a concept and its terms.
const (
	MaxTermRunes       = 200
	MaxTermsPerConcept = 200
	MaxDefinitionRunes = 4000
	MaxNoteRunes       = 4000
	MaxDomainRunes     = 100
	MaxProductRefRunes = 200
)

// Term is one way to say a concept in one locale.
type Term struct {
	ID            uuid.UUID
	Locale        bcp47.Tag
	Text          string
	Status        TermStatus
	PartOfSpeech  PartOfSpeech
	CaseSensitive bool
	Note          string
}

// Concept is a termbase entry (intent §19.2): what something means,
// and the terms that say it per locale. It is the aggregate root; its
// terms change only through it, and every change is a new version.
type Concept struct {
	ID uuid.UUID
	// ProjectID scopes the concept to one project; nil is tenant-wide.
	ProjectID  *uuid.UUID
	Definition string
	Domain     string
	Note       string
	// ProductRef links a product concept (intent §18), opaque for now.
	ProductRef string
	Terms      []Term
	Version    int
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedBy  string
	UpdatedAt  time.Time
}

// ConceptInput is a concept's content as written.
type ConceptInput struct {
	Definition string
	Domain     string
	Note       string
	ProductRef string
	Terms      []TermInput
}

// TermInput is a term as written.
type TermInput struct {
	Locale        string
	Text          string
	Status        string
	PartOfSpeech  string
	CaseSensitive bool
	Note          string
}

// NewConcept validates in and creates version 1.
func NewConcept(id uuid.UUID, project *uuid.UUID, in ConceptInput, by string, now time.Time) (Concept, error) {
	c := Concept{ID: id, ProjectID: project, CreatedBy: by, CreatedAt: now}
	if err := c.apply(in, nil, by, now); err != nil {
		return Concept{}, err
	}
	return c, nil
}

// Replace replaces the concept's content (terms included) and bumps its
// version. It reports false when nothing changed.
func (c *Concept) Replace(in ConceptInput, by string, now time.Time) (bool, error) {
	next := *c
	if err := next.apply(in, c.Terms, by, now); err != nil {
		return false, err
	}
	if c.sameContent(next) {
		return false, nil
	}
	*c = next
	return true, nil
}

func (c *Concept) apply(in ConceptInput, old []Term, by string, now time.Time) error {
	fields := []struct {
		name, value string
		max         int
	}{
		{"definition", in.Definition, MaxDefinitionRunes}, {"domain", in.Domain, MaxDomainRunes},
		{"note", in.Note, MaxNoteRunes}, {"product_ref", in.ProductRef, MaxProductRefRunes},
	}
	for _, f := range fields {
		if utf8.RuneCountInString(f.value) > f.max {
			return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidConcept, f.name, f.max)
		}
	}
	if len(in.Terms) == 0 || len(in.Terms) > MaxTermsPerConcept {
		return fmt.Errorf("%w: a concept has 1 to %d terms", ErrInvalidConcept, MaxTermsPerConcept)
	}
	seen := map[string]bool{}
	terms := make([]Term, 0, len(in.Terms))
	for i, ti := range in.Terms {
		t, err := newTerm(ti)
		if err != nil {
			return fmt.Errorf("terms[%d]: %w", i, err)
		}
		key := t.Locale.String() + "\x00" + fold(t.Text, t.Locale)
		if seen[key] {
			return fmt.Errorf("%w: %s %q", ErrDuplicateTerm, t.Locale, t.Text)
		}
		seen[key] = true
		// Keep the IDs of terms that stay, so history and hits stay stable.
		t.ID = termID(old, t)
		terms = append(terms, t)
	}
	c.Definition, c.Domain = strings.TrimSpace(in.Definition), strings.TrimSpace(in.Domain)
	c.Note, c.ProductRef = strings.TrimSpace(in.Note), strings.TrimSpace(in.ProductRef)
	c.Terms = terms
	c.Version++
	c.UpdatedBy, c.UpdatedAt = by, now
	return nil
}

// termID returns the ID of the existing term with t's locale and text,
// or a new one.
func termID(old []Term, t Term) uuid.UUID {
	for _, o := range old {
		if o.Locale == t.Locale && o.Text == t.Text {
			return o.ID
		}
	}
	return uuid.Must(uuid.NewV7())
}

func (c Concept) sameContent(o Concept) bool {
	if c.Definition != o.Definition || c.Domain != o.Domain || c.Note != o.Note || c.ProductRef != o.ProductRef ||
		len(c.Terms) != len(o.Terms) {
		return false
	}
	for i := range c.Terms {
		if c.Terms[i] != o.Terms[i] {
			return false
		}
	}
	return true
}

func newTerm(in TermInput) (Term, error) {
	loc, err := bcp47.Parse(in.Locale)
	if err != nil {
		return Term{}, err
	}
	text := strings.TrimSpace(in.Text)
	if text == "" || utf8.RuneCountInString(text) > MaxTermRunes || strings.ContainsFunc(text, unicode.IsControl) {
		return Term{}, fmt.Errorf("%w: text must be 1 to %d characters on one line", ErrInvalidTerm, MaxTermRunes)
	}
	if !strings.ContainsFunc(text, isWordRune) {
		return Term{}, fmt.Errorf("%w: %q has no letters or digits", ErrInvalidTerm, text)
	}
	status, err := ParseTermStatus(in.Status)
	if err != nil {
		return Term{}, err
	}
	pos, err := ParsePartOfSpeech(in.PartOfSpeech)
	if err != nil {
		return Term{}, err
	}
	if utf8.RuneCountInString(in.Note) > MaxNoteRunes {
		return Term{}, fmt.Errorf("%w: note is longer than %d characters", ErrInvalidTerm, MaxNoteRunes)
	}
	return Term{
		Locale: loc, Text: text, Status: status, PartOfSpeech: pos, CaseSensitive: in.CaseSensitive,
		Note: strings.TrimSpace(in.Note),
	}, nil
}

// TermsIn returns the concept's terms that apply to text in locale: a
// term's locale is locale or one of its truncations (a de term applies
// to de-AT text, not the other way round).
func (c Concept) TermsIn(locale bcp47.Tag) []Term {
	var out []Term
	for _, t := range c.Terms {
		if appliesTo(t.Locale, locale) {
			out = append(out, t)
		}
	}
	return out
}

func appliesTo(term, text bcp47.Tag) bool {
	if term == text {
		return true
	}
	for _, t := range text.Truncations() {
		if t == term {
			return true
		}
	}
	return false
}
