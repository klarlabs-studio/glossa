package layers

import (
	"sort"
	"strings"
	"unicode/utf8"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The literal text of a message, and how a finding points back into it.
//
// Four layers — style, length, locale and source — grade what a message
// *says* rather than what it declares, and all four have the same two
// problems. The first is that a message is a model and not a string: a
// select message has one pattern per variant, and a pattern is text,
// placeholders and markup interleaved. The second is that a finding's
// span is in bytes of the message *as it was authored*, which is the
// string a person edits, not the concatenation of its Text elements.
//
// So: scanning happens over the literal text, where `{$count}` is not a
// brace anybody typed and a markup element is not an angle bracket; and
// a span is resolved afterwards by locating the offending run in the
// authored text, when the caller carried it and the run is there to be
// found. A caller that kept only the model gets findings with no span,
// which is the honest answer — a span into a string nobody has is worse
// than none.

// Variant is one pattern of a message, as a layer reads it: its literal
// text and the variant keys that select it.
type Variant struct {
	// Keys names the variant ("one", "other"), joined by a space for a
	// multi-selector message. It is empty for a pattern message.
	Keys string
	// Text is the pattern's literal text, with placeholders and markup
	// removed.
	Text string
}

// Variants are msg's patterns as literal text, in the model's order. A
// nil message has none.
func Variants(msg *mf.Message) []Variant {
	if msg == nil {
		return nil
	}
	out := make([]Variant, 0, len(msg.Variants)+1)
	if !msg.IsSelect() {
		return append(out, Variant{Text: literal(msg.Pattern)})
	}
	for _, v := range msg.Variants {
		keys := make([]string, 0, len(v.Keys))
		for _, k := range v.Keys {
			if k.Catchall {
				keys = append(keys, "*")
				continue
			}
			keys = append(keys, k.Value)
		}
		out = append(out, Variant{Keys: strings.Join(keys, " "), Text: literal(v.Value)})
	}
	return out
}

// literal is a pattern's Text elements, concatenated.
func literal(p mf.Pattern) string {
	var b strings.Builder
	for _, el := range p {
		if t, ok := el.(mf.Text); ok {
			b.WriteString(string(t))
		}
	}
	return b.String()
}

// RenderedLength is the message's literal length in characters: the
// longest of its patterns, because that is the one that has to fit.
//
// Placeholders count as nothing. A layer cannot know what `{$count}`
// renders as without rendering it, and a check that guessed would be
// wrong in exactly the locales it matters in.
func RenderedLength(msg *mf.Message) int {
	longest := 0
	for _, v := range Variants(msg) {
		longest = max(longest, utf8.RuneCountInString(v.Text))
	}
	return longest
}

// LongestVariant is the pattern RenderedLength measured, which is the
// one a length finding is about.
func LongestVariant(msg *mf.Message) Variant {
	var out Variant
	n := -1
	for _, v := range Variants(msg) {
		if got := utf8.RuneCountInString(v.Text); got > n {
			out, n = v, got
		}
	}
	return out
}

// Placeholders are the names of msg's expression operands and its
// function annotations, in a stable order: what the source layer reads
// to decide whether a message needs a description, and what the locale
// layer reads to decide whether a date was typed out beside one.
func Placeholders(msg *mf.Message) []string {
	if msg == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, p := range msg.Patterns() {
		for _, el := range p {
			e, ok := el.(mf.Expression)
			if !ok {
				continue
			}
			switch arg := e.Arg.(type) {
			case mf.VariableRef:
				seen[arg.Name] = true
			case mf.Literal:
				seen[arg.Value] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Functions are the names of the function annotations msg uses
// (`number`, `date`), in a stable order. A typed placeholder is what
// `hardcoded-format` asks the author to reach for.
func Functions(msg *mf.Message) []string {
	if msg == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, p := range msg.Patterns() {
		for _, el := range p {
			if e, ok := el.(mf.Expression); ok && e.Function != nil {
				seen[e.Function.Name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HasSelector reports whether msg chooses between variants, which is
// what a plural written out by hand is missing.
func HasSelector(msg *mf.Message) bool {
	return msg != nil && msg.IsSelect() && len(msg.Selectors) > 0
}

// SpanOf locates run in the authored text and answers with the span a
// finding carries, or nil when the caller kept no text or the run is
// not in it verbatim (a translation authored in MF1 whose literal text
// the parser normalized, say).
//
// The first occurrence wins. A run that appears twice is one finding
// about one message either way — the fingerprint is over the code, the
// locus and the subject, never the span — and pointing at the first is
// more use to a reader than pointing at neither.
func SpanOf(authored, run string, side domain.Side) *domain.Span {
	if authored == "" || run == "" {
		return nil
	}
	i := strings.Index(authored, run)
	if i < 0 {
		return nil
	}
	return &domain.Span{Side: side, Start: i, End: i + len(run)}
}

// words splits s into runs of letters and digits, which is what "a
// one-word message" counts and what a pronoun is matched as. Splitting
// on a character class rather than on spaces is what keeps "Sie," and
// "(Sie)" the word `Sie`.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isWordRune(r) })
}

func isWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '\'' || r == '’':
		// An apostrophe inside a word keeps `l'utilisateur` one word and
		// `d'accord` one word, which is what French needs of a word
		// count.
		return true
	case r > 0x7f:
		return isLetterish(r)
	}
	return false
}

// isLetterish is "a letter or a mark or a digit" without dragging in a
// table: everything above ASCII that is not a space and not one of the
// punctuation blocks a sentence is built from.
func isLetterish(r rune) bool {
	switch {
	case r == ' ' || r == ' ' || r == ' ' || r == ' ':
		return false // the no-break spaces the locale layer is about
	case r >= ' ' && r <= '⁯':
		return false // general punctuation, including the bidi controls
	case r >= '　' && r <= '〿':
		return false // CJK punctuation
	}
	return true
}

// findWord finds word in s as a whole word and answers with it as s
// wrote it — `Deine` for `deine` — because a span has to point at
// something that is in the text. fold matches case-insensitively, which
// is what the locales whose pronouns are capitalized mid-sentence need
// turned off.
func findWord(s, word string, fold bool) (string, bool) {
	for _, w := range words(s) {
		if w == word || (fold && strings.EqualFold(w, word)) {
			return w, true
		}
	}
	return "", false
}
