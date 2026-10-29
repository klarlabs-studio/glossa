package domain

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// Term recognition (RFC 0003 §2.2) finds a termbase's terms in a text.
// It is deterministic and dictionary-free:
//
//   - Text is split into words: runs of letters, marks and digits.
//     Everything else separates words, so hyphens and apostrophes do too
//     ("Arbeitsbereichs-Name", "l'espace"); a change between a script
//     written without spaces and one written with them also does
//     ("Glossaのアカウント").
//   - Case is folded per rune with the locale's rules (Turkish and
//     Azerbaijani dotted and dotless i), unless a term is case-sensitive.
//     Folding is per rune, not full Unicode case folding: "STRASSE" does
//     not match "Straße".
//   - A term matches whole words from a word start. Each of its words
//     may carry a short inflectional ending: up to 3 more letters for
//     words of 5+ letters, 2 for 4-letter words, none below
//     (workspace → workspaces, Rechnung → Rechnungen, carte bancaire →
//     cartes bancaires; art ≠ arts). That is inflection tolerance, not
//     stemming: ablaut (Konto → Konten) and compounds
//     (Rechnungsadresse) are not recognized. Stemming is a later port.
//   - Hangul words take up to 3 more syllables (particles) from 2
//     syllables on.
//   - Terms in scripts written without spaces (Han, Hiragana, Katakana,
//     Thai, Lao, Khmer, Myanmar, Tibetan) match as substrings anywhere,
//     because those texts have no word boundaries to respect. A short
//     term can then match inside a longer word (会議 in 会議室) — the
//     accepted cost of not segmenting words, until a segmenter port.
//   - Overlapping matches resolve leftmost-longest; terms of different
//     concepts on exactly the same span (homonyms) are all kept.
//
// Offsets are byte offsets into the text, end exclusive.

// TermHit is a term found in a text.
type TermHit struct {
	ConceptID uuid.UUID
	Term      Term
	// Start and End delimit the match in the text (bytes, End
	// exclusive); Text is text[Start:End].
	Start int
	End   int
	Text  string
}

// Termbase is a set of concepts ready for recognition.
type Termbase struct {
	concepts []Concept
	byID     map[uuid.UUID]int
	entries  []entry
}

type entry struct {
	concept    int
	term       Term
	words      []word // token mode
	runes      []rune // substring mode, folded unless case-sensitive
	continuous bool
}

// NewTermbase prepares concepts for recognition. Order doesn't matter:
// results are sorted.
func NewTermbase(concepts []Concept) *Termbase {
	cs := slices.Clone(concepts)
	slices.SortFunc(cs, func(a, b Concept) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	tb := &Termbase{concepts: cs, byID: make(map[uuid.UUID]int, len(cs))}
	for i, c := range cs {
		tb.byID[c.ID] = i
		for _, t := range c.Terms {
			e := entry{concept: i, term: t, continuous: strings.ContainsFunc(t.Text, isContinuousScript)}
			if e.continuous {
				e.runes = []rune(t.Text)
				if !t.CaseSensitive {
					e.runes = foldRunes(e.runes, t.Locale)
				}
			} else {
				e.words = words(t.Text, t.Locale)
			}
			tb.entries = append(tb.entries, e)
		}
	}
	return tb
}

// Concepts returns the termbase's concepts, by ID.
func (tb *Termbase) Concepts() []Concept { return tb.concepts }

// Concept returns a concept by ID.
func (tb *Termbase) Concept(id uuid.UUID) (Concept, bool) {
	i, ok := tb.byID[id]
	if !ok {
		return Concept{}, false
	}
	return tb.concepts[i], true
}

// Recognize finds every term applying to locale (see Concept.TermsIn),
// whatever its status, in text.
func (tb *Termbase) Recognize(text string, locale bcp47.Tag) []TermHit {
	return tb.recognize(text, locale, func(Term) bool { return true })
}

// recognize finds the terms keep accepts.
func (tb *Termbase) recognize(text string, locale bcp47.Tag, keep func(Term) bool) []TermHit {
	ws := words(text, locale)
	src := textRunes(text, locale)
	var cands []TermHit
	for _, e := range tb.entries {
		if !appliesTo(e.term.Locale, locale) || !keep(e.term) {
			continue
		}
		id := tb.concepts[e.concept].ID
		if e.continuous {
			for _, span := range src.find(e.runes, e.term.CaseSensitive) {
				cands = append(cands, TermHit{ConceptID: id, Term: e.term, Start: span[0], End: span[1]})
			}
			continue
		}
		for i := 0; i+len(e.words) <= len(ws); i++ {
			if matchWords(ws[i:i+len(e.words)], e.words, e.term.CaseSensitive) {
				cands = append(cands, TermHit{ConceptID: id, Term: e.term, Start: ws[i].start, End: ws[i+len(e.words)-1].end})
			}
		}
	}
	return selectHits(text, cands)
}

// selectHits resolves overlaps leftmost-longest, keeping every hit on a
// kept span, in a deterministic order.
func selectHits(text string, cands []TermHit) []TermHit {
	slices.SortFunc(cands, func(a, b TermHit) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		if a.End != b.End {
			return b.End - a.End
		}
		if c := strings.Compare(a.ConceptID.String(), b.ConceptID.String()); c != 0 {
			return c
		}
		return strings.Compare(a.Term.ID.String(), b.Term.ID.String())
	})
	var out []TermHit
	lastStart, lastEnd := -1, -1
	for _, c := range cands {
		switch {
		case c.Start == lastStart && c.End == lastEnd:
			if last := out[len(out)-1]; last.ConceptID == c.ConceptID && last.Term.ID == c.Term.ID {
				continue
			}
		case c.Start < lastEnd:
			continue
		}
		lastStart, lastEnd = c.Start, c.End
		c.Text = text[c.Start:c.End]
		out = append(out, c)
	}
	return out
}

// word is one word of a text or term.
type word struct {
	start, end int // bytes
	raw        []rune
	folded     []rune
	hangul     bool
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) }

var continuousScripts = []*unicode.RangeTable{
	unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer,
	unicode.Myanmar, unicode.Tibetan,
}

func isContinuousScript(r rune) bool { return unicode.IsOneOf(continuousScripts, r) }

// words splits s into words (see the package comment on recognition).
func words(s string, locale bcp47.Tag) []word {
	var (
		out   []word
		cur   *word
		class bool
	)
	for i, r := range s {
		if !isWordRune(r) {
			cur = nil
			continue
		}
		c := isContinuousScript(r)
		// Marks belong to the word they follow, whatever their script.
		if cur != nil && c != class && !unicode.IsMark(r) {
			cur = nil
		}
		if cur == nil {
			out = append(out, word{start: i})
			cur, class = &out[len(out)-1], c
		}
		cur.raw = append(cur.raw, r)
		cur.end = i + utf8.RuneLen(r)
		if unicode.Is(unicode.Hangul, r) {
			cur.hangul = true
		}
	}
	for i := range out {
		out[i].folded = foldRunes(out[i].raw, locale)
	}
	return out
}

// Inflection tolerance (see the package comment on recognition).
const (
	maxEnding       = 3
	shortStem       = 4 // a 4-letter word takes a 2-letter ending
	minHangulStem   = 2
	shortStemEnding = 2
)

func matchWords(text, term []word, caseSensitive bool) bool {
	for i := range term {
		a, b := text[i].folded, term[i].folded
		if caseSensitive {
			a, b = text[i].raw, term[i].raw
		}
		if !matchWord(a, b, term[i].hangul) {
			return false
		}
	}
	return true
}

// matchWord reports whether text is the term word, possibly with a
// short ending.
func matchWord(text, term []rune, hangul bool) bool {
	if len(text) < len(term) || !slices.Equal(text[:len(term)], term) {
		return false
	}
	extra := len(text) - len(term)
	switch {
	case extra == 0:
		return true
	case hangul:
		return len(term) >= minHangulStem && extra <= maxEnding
	case len(term) > shortStem:
		return extra <= maxEnding
	case len(term) == shortStem:
		return extra <= shortStemEnding
	}
	return false
}

// runeText is a text as runes, raw and folded, with each rune's byte
// offset, for substring matching.
type runeText struct {
	raw, folded []rune
	offsets     []int // offsets[i] is rune i's byte offset; offsets[len] is len(text)
}

func textRunes(s string, locale bcp47.Tag) runeText {
	rt := runeText{}
	for i, r := range s {
		rt.raw = append(rt.raw, r)
		rt.offsets = append(rt.offsets, i)
	}
	rt.offsets = append(rt.offsets, len(s))
	rt.folded = foldRunes(rt.raw, locale)
	return rt
}

// find returns the byte spans of every occurrence of needle.
func (rt runeText) find(needle []rune, caseSensitive bool) [][2]int {
	hay := rt.folded
	if caseSensitive {
		hay = rt.raw
	}
	var out [][2]int
	for i := 0; len(needle) > 0 && i+len(needle) <= len(hay); i++ {
		if slices.Equal(hay[i:i+len(needle)], needle) {
			out = append(out, [2]int{rt.offsets[i], rt.offsets[i+len(needle)]})
		}
	}
	return out
}

// foldRunes lowercases rune by rune (so offsets survive), with Turkish
// casing for tr and az.
func foldRunes(rs []rune, locale bcp47.Tag) []rune {
	lower := unicode.ToLower
	if base, _, _ := strings.Cut(locale.String(), "-"); base == "tr" || base == "az" {
		lower = unicode.TurkishCase.ToLower
	}
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = lower(r)
	}
	return out
}

// fold is foldRunes for a string (duplicate detection).
func fold(s string, locale bcp47.Tag) string { return string(foldRunes([]rune(s), locale)) }
