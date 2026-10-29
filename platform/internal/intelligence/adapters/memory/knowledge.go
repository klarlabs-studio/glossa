// Package memory holds in-memory implementations of the Intelligence
// ports: a Knowledge fake (translation memory, termbase, style guides,
// message context) and a budget guard. Tests and the offline evals use
// them; production wiring adapts the Knowledge context instead.
//
// The fake follows RFC 0003 §2's rules closely enough to be meaningful —
// exact matches by canonical MF2 with variable names normalized, fuzzy
// matches by character-trigram similarity, term recognition by
// case-folded word-prefix match, and the term_missing / term_forbidden
// QA rules — without pretending to be the real matcher.
package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// TMUnit is one stored translation unit.
type TMUnit struct {
	ID     string            `json:"id"`
	Pair   domain.LocalePair `json:"pair"`
	Key    string            `json:"key,omitempty"`
	Source string            `json:"source"` // MF2
	Target string            `json:"target"` // MF2
}

// Concept is a termbase concept with its terms in every locale.
type Concept struct {
	ID         string        `json:"id"`
	Definition string        `json:"definition,omitempty"`
	Terms      []domain.Term `json:"terms"`
}

// Knowledge is an in-memory domain.Knowledge for one tenant. It ignores
// the project scope: everything is tenant-wide.
type Knowledge struct {
	mu       sync.RWMutex
	tenant   string
	units    []TMUnit
	concepts []Concept
	styles   map[string]domain.StyleGuide // by target locale
	contexts map[string]domain.MessageContext
}

var _ domain.Knowledge = (*Knowledge)(nil)

// ErrForeignTenant is returned for a scope of another tenant.
var ErrForeignTenant = errors.New("memory knowledge: scope belongs to another tenant")

// NewKnowledge returns an empty store for tenant.
func NewKnowledge(tenant string) *Knowledge {
	return &Knowledge{tenant: tenant, styles: map[string]domain.StyleGuide{}, contexts: map[string]domain.MessageContext{}}
}

// AddUnits stores translation units.
func (k *Knowledge) AddUnits(units ...TMUnit) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.units = append(k.units, units...)
}

// AddConcepts stores termbase concepts.
func (k *Knowledge) AddConcepts(concepts ...Concept) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.concepts = append(k.concepts, concepts...)
}

// SetStyle sets the effective style guide of a locale.
func (k *Knowledge) SetStyle(locale string, g domain.StyleGuide) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.styles[locale] = g
}

// SetContext stores a message's context.
func (k *Knowledge) SetContext(c domain.MessageContext) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.contexts[c.MessageID] = c
}

func (k *Knowledge) check(scope domain.Scope) error {
	if scope.TenantID != k.tenant {
		return ErrForeignTenant
	}
	return nil
}

// LookupTM implements domain.TranslationMemory.
func (k *Knowledge) LookupTM(_ context.Context, scope domain.Scope, q domain.TMQuery) ([]domain.TMMatch, error) {
	if err := k.check(scope); err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	norm := normalizeVariables(q.Source)
	text := plain(q.Source)
	var out []domain.TMMatch
	for _, u := range k.units {
		if !strings.EqualFold(u.Pair.Source, q.Pair.Source) || !strings.EqualFold(u.Pair.Target, q.Pair.Target) {
			continue
		}
		score := 0
		if normalizeVariables(u.Source) == norm {
			score = domain.ScoreExact
			if q.Key != "" && u.Key == q.Key {
				score = domain.ScoreInContextExact
			}
		} else if sim := similarity(text, plain(u.Source)); sim >= 0.5 {
			score = min(99, domain.ScoreFuzzyMin+int(math.Round((sim-0.5)/0.5*49)))
		}
		if score > 0 {
			out = append(out, domain.TMMatch{UnitID: u.ID, Score: score, Source: u.Source, Target: u.Target, Origin: "approved_revision"})
		}
	}
	slices.SortStableFunc(out, func(a, b domain.TMMatch) int { return b.Score - a.Score })
	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}
	return out[:min(len(out), limit)], nil
}

// RecognizeTerms implements domain.Termbase.
func (k *Knowledge) RecognizeTerms(_ context.Context, scope domain.Scope, pair domain.LocalePair, sourceText string) ([]domain.TermHit, error) {
	if err := k.check(scope); err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	var out []domain.TermHit
	for _, c := range k.concepts {
		for _, t := range c.Terms {
			if !sameLanguage(t.Locale, pair.Source) || !t.Status.Usable() || !contains(sourceText, t, pair.Source) {
				continue
			}
			hit := domain.TermHit{ConceptID: c.ID, Definition: c.Definition, Source: t}
			for _, tt := range c.Terms {
				if sameLanguage(tt.Locale, pair.Target) {
					hit.Targets = append(hit.Targets, tt)
				}
			}
			out = append(out, hit)
			break
		}
	}
	return out, nil
}

// CheckTerminology implements domain.Termbase with the RFC 0003 §2.2
// rules.
func (k *Knowledge) CheckTerminology(ctx context.Context, scope domain.Scope, pair domain.LocalePair, sourceText, translationText string) ([]domain.TermFinding, error) {
	hits, err := k.RecognizeTerms(ctx, scope, pair, sourceText)
	if err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	var out []domain.TermFinding
	for _, h := range hits {
		var usable []domain.Term
		for _, t := range h.Targets {
			if t.Status.Usable() {
				usable = append(usable, t)
			}
		}
		if len(usable) > 0 && !slices.ContainsFunc(usable, func(t domain.Term) bool { return contains(translationText, t, pair.Target) }) {
			out = append(out, domain.TermFinding{
				Code: domain.FindingTermMissing, ConceptID: h.ConceptID, TermID: usable[0].ID, Term: usable[0].Text,
				Message: fmt.Sprintf("%q is in the source, but the translation uses none of its %s terms (preferred: %q)", h.Source.Text, pair.Target, usable[0].Text),
			})
		}
	}
	for _, c := range k.concepts {
		for _, t := range c.Terms {
			if sameLanguage(t.Locale, pair.Target) && !t.Status.Usable() && contains(translationText, t, pair.Target) {
				out = append(out, domain.TermFinding{
					Code: domain.FindingTermForbidden, ConceptID: c.ID, TermID: t.ID, Term: t.Text,
					Message: fmt.Sprintf("the translation uses %q, which is %s", t.Text, t.Status),
				})
			}
		}
	}
	return out, nil
}

// EffectiveStyle implements domain.StyleGuides.
func (k *Knowledge) EffectiveStyle(_ context.Context, scope domain.Scope, locale, _ string) (domain.StyleGuide, error) {
	if err := k.check(scope); err != nil {
		return domain.StyleGuide{}, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if g, ok := k.styles[locale]; ok {
		return g, nil
	}
	lang, _, _ := strings.Cut(locale, "-")
	return k.styles[lang], nil
}

// MessageContext implements domain.MessageContexts.
func (k *Knowledge) MessageContext(_ context.Context, scope domain.Scope, messageID, _ string) (domain.MessageContext, error) {
	if err := k.check(scope); err != nil {
		return domain.MessageContext{}, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if c, ok := k.contexts[messageID]; ok {
		return c, nil
	}
	return domain.MessageContext{MessageID: messageID}, nil
}

var variable = regexp.MustCompile(`\$[\p{L}\p{N}_.-]+`)

// normalizeVariables renames variables to $1, $2… in order of
// appearance, so matches ignore variable names but keep structure.
func normalizeVariables(src string) string {
	names := map[string]string{}
	return variable.ReplaceAllStringFunc(src, func(v string) string {
		if n, ok := names[v]; ok {
			return n
		}
		names[v] = fmt.Sprintf("$%d", len(names)+1)
		return names[v]
	})
}

var expression = regexp.MustCompile(`\{[^{}]*\}`)

// plain is a rough plain text of MF2 syntax for similarity.
func plain(src string) string {
	return strings.Join(strings.Fields(expression.ReplaceAllString(src, " ")), " ")
}

// similarity is the Dice coefficient of character trigrams.
func similarity(a, b string) float64 {
	ta, tb := trigrams(strings.ToLower(a)), trigrams(strings.ToLower(b))
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	common := 0
	for g, n := range ta {
		common += min(n, tb[g])
	}
	return 2 * float64(common) / float64(total(ta)+total(tb))
}

func trigrams(s string) map[string]int {
	r := []rune(" " + s + " ")
	out := map[string]int{}
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])]++
	}
	return out
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func sameLanguage(a, b string) bool {
	la, _, _ := strings.Cut(strings.ToLower(a), "-")
	lb, _, _ := strings.Cut(strings.ToLower(b), "-")
	return la == lb
}

// scriptio continua languages match terms as substrings.
var noSpaces = map[string]bool{"ja": true, "zh": true, "ko": true, "th": true}

// contains reports whether text uses term: substring for languages
// without spaces, else case-folded word-prefix match (simple inflection
// tolerance: "Datei" matches "Dateien").
func contains(text string, t domain.Term, locale string) bool {
	fold := func(s string) string {
		if t.CaseSensitive {
			return s
		}
		return strings.ToLower(s)
	}
	lang, _, _ := strings.Cut(strings.ToLower(locale), "-")
	if noSpaces[lang] {
		return strings.Contains(fold(text), fold(t.Text))
	}
	words := strings.FieldsFunc(fold(text), notWord)
	termWords := strings.FieldsFunc(fold(t.Text), notWord)
	if len(termWords) == 0 {
		return false
	}
	for i := 0; i+len(termWords) <= len(words); i++ {
		match := true
		for j, tw := range termWords {
			w := words[i+j]
			last := j == len(termWords)-1
			if (last && !strings.HasPrefix(w, tw)) || (!last && w != tw) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func notWord(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }
