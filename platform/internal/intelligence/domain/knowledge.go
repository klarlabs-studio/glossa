package domain

import "context"

// The Knowledge ports (RFC 0003 §2) are what the translation agent's
// tools read. They are deliberately minimal: the Knowledge context owns
// translation memory, termbase and style guides, and the wiring wave
// adapts its application services to these interfaces. Every call is
// scoped to one tenant and, optionally, one project; implementations must
// never return another tenant's data.
//
// Texts cross these ports as follows:
//   - TM queries carry the source as MF2 syntax; the Knowledge context
//     normalizes placeholders itself and returns the stored canonical
//     MF2 target.
//   - Term recognition and terminology QA get plain text: the literal
//     text of every pattern, placeholders and markup removed (PlainText).

// LocalePair is a translation direction.
type LocalePair struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// TMQuery asks translation memory for matches of one source message.
type TMQuery struct {
	Pair LocalePair `json:"pair"`
	// Source is the source message in MF2 syntax.
	Source string `json:"source"`
	// Key and Namespace let the Knowledge context score 101 (in-context
	// exact): a unit approved for the same key in the same namespace.
	Key       string `json:"key,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	// ExactOnly leaves fuzzy matches out.
	ExactOnly bool `json:"exact_only,omitempty"`
	// Uncounted looks without using: the matches' hit counts stay (a
	// fill preview).
	Uncounted bool `json:"uncounted,omitempty"`
}

// TM match score bands (RFC 0003 §2.1).
const (
	ScoreInContextExact = 101
	ScoreExact          = 100
	ScoreFuzzyMin       = 50
)

// TMMatch is one translation-memory match.
type TMMatch struct {
	UnitID string `json:"unit_id"`
	// Score is 101 (in-context exact), 100 (exact) or 50–99 (fuzzy).
	Score int `json:"score"`
	// Source is the matched unit's source, Target its canonical MF2 target.
	Source string `json:"source"`
	Target string `json:"target"`
	// Origin says where the unit came from ("approved_revision", "tmx").
	Origin string `json:"origin,omitempty"`
}

// IsExact reports an exact (100) or in-context exact (101) match.
func (m TMMatch) IsExact() bool { return m.Score >= ScoreExact }

// TranslationMemory looks up matches.
type TranslationMemory interface {
	LookupTM(ctx context.Context, scope Scope, q TMQuery) ([]TMMatch, error)
}

// TermStatus is a term's usage status (TBX-Basic).
type TermStatus string

// Term statuses.
const (
	TermPreferred  TermStatus = "preferred"
	TermAdmitted   TermStatus = "admitted"
	TermDeprecated TermStatus = "deprecated"
	TermForbidden  TermStatus = "forbidden"
)

// Usable reports whether a translation may use the term.
func (s TermStatus) Usable() bool { return s == TermPreferred || s == TermAdmitted }

// Term is one locale's designation of a concept.
type Term struct {
	ID            string     `json:"id"`
	Text          string     `json:"text"`
	Locale        string     `json:"locale"`
	Status        TermStatus `json:"status"`
	CaseSensitive bool       `json:"case_sensitive,omitempty"`
	PartOfSpeech  string     `json:"part_of_speech,omitempty"`
	Note          string     `json:"note,omitempty"`
}

// TermHit is a concept recognized in the source, with the target
// locale's terms for it.
type TermHit struct {
	ConceptID  string `json:"concept_id"`
	Definition string `json:"definition,omitempty"`
	// Source is the source-locale term that was recognized.
	Source Term `json:"source"`
	// Targets are the target locale's terms with their status.
	Targets []Term `json:"targets"`
}

// Terminology finding codes (RFC 0003 §2.2).
const (
	FindingTermMissing   = "term_missing"
	FindingTermForbidden = "term_forbidden"
)

// TermFinding is one terminology QA result.
type TermFinding struct {
	Code      string `json:"code"`
	ConceptID string `json:"concept_id"`
	// TermID and Term name the forbidden term used, or the preferred term
	// that is missing.
	TermID  string `json:"term_id,omitempty"`
	Term    string `json:"term"`
	Message string `json:"message"`
}

// Termbase recognizes terms and checks terminology.
type Termbase interface {
	RecognizeTerms(ctx context.Context, scope Scope, pair LocalePair, sourceText string) ([]TermHit, error)
	CheckTerminology(ctx context.Context, scope Scope, pair LocalePair, sourceText, translationText string) ([]TermFinding, error)
}

// StyleRule is one style-guide rule with its rationale and examples.
type StyleRule struct {
	ID        string   `json:"id"`
	Rule      string   `json:"rule"`
	Rationale string   `json:"rationale,omitempty"`
	Good      []string `json:"good,omitempty"`
	Bad       []string `json:"bad,omitempty"`
}

// Formality values.
const (
	FormalityFormal   = "formal"
	FormalityInformal = "informal"
)

// StyleGuide is the effective, merged style guide for a locale and
// namespace (tenant → project → locale → namespace, narrowest wins).
type StyleGuide struct {
	// Version identifies the merged guide for provenance.
	Version string `json:"version"`
	// Formality is "formal", "informal" or empty; Pronoun names the
	// form of address, e.g. "Sie" or "du".
	Formality   string            `json:"formality,omitempty"`
	Pronoun     string            `json:"pronoun,omitempty"`
	Tone        []string          `json:"tone,omitempty"`
	Punctuation map[string]string `json:"punctuation,omitempty"`
	Rules       []StyleRule       `json:"rules,omitempty"`
}

// StyleGuides resolves the effective style guide.
type StyleGuides interface {
	EffectiveStyle(ctx context.Context, scope Scope, locale, namespace string) (StyleGuide, error)
}

// Neighbour is a message near the one being translated (same key prefix).
type Neighbour struct {
	Key         string `json:"key"`
	Source      string `json:"source"`
	Translation string `json:"translation,omitempty"`
}

// Message policy tags (RFC 0003 §3.3, §7), set per namespace.
const (
	TagSensitive = "sensitive"
	TagLegal     = "legal"
	TagMarketing = "marketing"
)

// MessageContext is what a translator should know about a message beyond
// its text (RFC 0003 §2.4).
type MessageContext struct {
	MessageID   string      `json:"message_id"`
	Key         string      `json:"key"`
	Namespace   string      `json:"namespace,omitempty"`
	Description string      `json:"description,omitempty"`
	MaxLength   *int        `json:"max_length,omitempty"`
	Usages      []string    `json:"usages,omitempty"`
	Neighbours  []Neighbour `json:"neighbours,omitempty"`
	// Tags are the namespace's policy tags (sensitive, legal, marketing).
	Tags []string `json:"tags,omitempty"`
}

// MessageContexts reads message context from the Catalog (via Knowledge).
type MessageContexts interface {
	MessageContext(ctx context.Context, scope Scope, messageID, targetLocale string) (MessageContext, error)
}

// Knowledge is everything the translation agent's tools read.
type Knowledge interface {
	TranslationMemory
	Termbase
	StyleGuides
	MessageContexts
}
