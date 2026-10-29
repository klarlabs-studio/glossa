package prompts

// Data is what every template renders. It is filled from the tool
// results only, so a prompt contains exactly the knowledge the agent's
// tools returned (RFC 0003 §7) and nothing else.
type Data struct {
	SourceLocale string
	TargetLocale string
	// Source is the source message in MF2 syntax.
	Source    string
	Arguments []Argument
	Markup    []string
	// PluralCategories are the target locale's CLDR cardinal categories,
	// set when the source selects by plural; OrdinalCategories likewise.
	PluralCategories  []string
	OrdinalCategories []string
	Context           Context
	TM                []TMMatch
	Terms             []Term
	Style             Style

	// Repair turn.
	Attempt     int
	MaxAttempts int
	Findings    []Finding

	// Assess.
	Translation string

	// Linguistic review (RFC 0005 §3.8). SourceText and TranslationText
	// are the literal text of the two messages — placeholders and markup
	// removed, patterns one per line — because that is what the model
	// quotes from and what the resulting spans are offsets into. Codes
	// is the layer's whole vocabulary, and MaxFindings the most notes
	// one answer may carry.
	SourceText      string
	TranslationText string
	Codes           []Code
	MaxFindings     int
}

// Code is one finding code the linguistic reviewer may report, with
// what it covers. The layer owns the list; the prompt only renders it.
type Code struct {
	Code    string
	Meaning string
}

// Argument is a message argument.
type Argument struct {
	Name     string
	Type     string
	Selector string // "", "plural", "ordinal", "exact", "string"
	Keys     []string
}

// Context is the message context.
type Context struct {
	Key         string
	Namespace   string
	Description string
	MaxLength   int // 0 when unlimited
	Usages      []string
	Neighbours  []Neighbour
}

// Neighbour is a nearby message.
type Neighbour struct {
	Key         string
	Source      string
	Translation string
}

// TMMatch is a translation-memory match.
type TMMatch struct {
	Score  int
	Source string
	Target string
}

// Term is a glossary entry for a concept found in the source.
type Term struct {
	Source     string
	Definition string
	// Use lists preferred then admitted target terms; Avoid lists
	// forbidden and deprecated ones.
	Use   []string
	Avoid []string
}

// Style is the effective style guide.
type Style struct {
	Formality   string
	Pronoun     string
	Tone        []string
	Punctuation []KeyValue
	Rules       []Rule
}

// KeyValue is one punctuation or typography preference.
type KeyValue struct{ Key, Value string }

// Rule is one style rule.
type Rule struct {
	Rule      string
	Rationale string
	Good      []string
	Bad       []string
}

// Finding is a problem the repair turn asks the model to fix.
type Finding struct {
	Code    string
	Subject string
	Message string
}

// Empty reports whether the style guide says nothing.
func (s Style) Empty() bool {
	return s.Formality == "" && s.Pronoun == "" && len(s.Tone) == 0 && len(s.Punctuation) == 0 && len(s.Rules) == 0
}

// HasContext reports whether there is any message context to show.
func (c Context) HasContext() bool {
	return c.Key != "" || c.Description != "" || c.MaxLength > 0 || len(c.Usages) > 0 || len(c.Neighbours) > 0
}
