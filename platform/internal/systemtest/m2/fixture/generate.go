package fixture

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	mf "go.klarlabs.de/glossa/messageformat"

	idomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	kdomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

const sourceLocale = "de"

var (
	targetLocales = []string{"en", "es", "fr", "ja"}
	fillLocales   = []string{"es", "fr", "ja"}
)

// CatalogSize is the number of messages.
const CatalogSize = 600

// coverage is the share of the non-sensitive messages translated (and
// approved) before the fill; sensitiveCoverage the share of the legal
// clauses, which only people translate.
var (
	coverage          = map[string]float64{"en": 1, "es": 0.70, "fr": 0.50, "ja": 0}
	sensitiveCoverage = map[string]float64{"en": 1, "es": 1, "fr": 0.5, "ja": 0}
)

// slipRates are the deliberate mistakes per locale, as shares of the
// messages the provider drafts (each at least one). pluralSlipShare is
// the share of the drafted es/fr plural messages that lose `many`.
var slipRates = []struct {
	kind string
	rate float64
}{
	{SlipForbiddenTerm, 0.02},
	{SlipFormality, 0.015},
	{SlipTooLong, 0.01},
	{SlipPlaceholderRepaired, 0.015},
	{SlipPlaceholderPersistent, 0.01},
}

const pluralSlipShare = 0.15

// The tenant's legacy translation memory (TMX): exact units for
// messages missing in a locale, near variants of buttons, and legacy
// units with terminology the termbase now forbids.
var (
	legacyExact     = map[string]int{"es": 6, "fr": 10, "ja": 42}
	legacyForbidden = map[string]int{"ja": 8}
	legacyFuzzy     = map[string]int{"ja": 40}
)

// formalPronoun is the form of address of each target's style guide.
var formalPronoun = map[string]string{"es": "usted", "fr": "vous", "ja": "です・ます"}

// Too-long slips append an eager explanation.
var tooLongSuffix = map[string]string{
	"es": " (haga clic aquí para continuar con la acción seleccionada)",
	"fr": " (cliquez ici pour poursuivre l’action sélectionnée)",
	"ja": "（こちらのボタンをクリックすると、現在選択されている操作をそのまま続行して最後まで完了できます）",
}

// rank orders items deterministically by seed and purpose, independent
// of any RNG implementation.
func rank(seed int64, purpose, key string) uint64 {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d/%s/%s", seed, purpose, key))
	return binary.BigEndian.Uint64(sum[:8])
}

// pick returns the k items of keys ranked first for purpose.
func pick(seed int64, purpose string, keys []string, k int) map[string]bool {
	sorted := slices.Clone(keys)
	slices.SortFunc(sorted, func(a, b string) int {
		return cmp.Compare(rank(seed, purpose, a), rank(seed, purpose, b))
	})
	out := map[string]bool{}
	for _, key := range sorted[:min(k, len(sorted))] {
		out[key] = true
	}
	return out
}

func round(v float64) int { return int(math.Round(v)) }

// parsed is a message with its models.
type parsed struct {
	d      draft
	msg    Message
	source mf.Message
	models map[string]mf.Message // reference translations
}

type generator struct {
	seed     int64
	errs     []error
	items    []*parsed
	byKey    map[string]*parsed
	termbase *kdomain.Termbase
	concepts map[string]concept
}

func (g *generator) fail(format string, args ...any) {
	g.errs = append(g.errs, fmt.Errorf(format, args...))
}

// Generate builds the fixture from seed and checks that every reference
// translation is clean — structurally compatible, within max_length, free
// of terminology findings, routed approve_recommended by the default
// scoring without TM support — and that every slip has the effect it is
// labelled with.
func Generate(seed int64) (*Fixture, error) {
	g := &generator{seed: seed, byKey: map[string]*parsed{}, concepts: map[string]concept{}}
	g.parseAll(g.drafts())
	g.buildTermbase()
	f := &Fixture{
		Seed: seed, SourceLocale: sourceLocale, Locales: targetLocales, FillLocales: fillLocales,
		SensitiveNamespaces: []string{"legal"}, Expect: map[string]LocaleExpectation{},
	}
	g.chooseExisting()
	f.LegacyTM = g.legacyMemory()
	for _, l := range fillLocales {
		f.Expect[l] = g.route(l, f.LegacyTM)
	}
	g.validate()
	for _, it := range g.items {
		f.Messages = append(f.Messages, it.msg)
		for _, l := range fillLocales {
			if t := it.msg.Translations[l]; t != nil && t.Route == RouteAI && t.Slip == "" && t.LengthFlagged {
				e := f.Expect[l]
				e.LengthFlagged++
				f.Expect[l] = e
			}
		}
	}
	f.Concepts = g.fixtureConcepts()
	f.StyleGuides = styleGuides()
	if len(g.errs) > 0 {
		return nil, errors.Join(g.errs...)
	}
	return f, nil
}

// drafts renders every candidate message and trims the catalog to
// CatalogSize by dropping success toasts.
func (g *generator) drafts() []draft {
	var all []draft
	for i := range nouns {
		all = append(all, nounDrafts(&nouns[i])...)
	}
	all = append(all, actionDrafts()...)
	all = append(all, legalDrafts()...)
	excess := len(all) - CatalogSize
	if excess < 0 {
		g.fail("only %d candidate messages, want %d", len(all), CatalogSize)
		return all
	}
	var toasts []string
	for _, d := range all {
		if d.pattern == "success" {
			toasts = append(toasts, d.key)
		}
	}
	drop := pick(g.seed, "trim", toasts, excess)
	return slices.DeleteFunc(all, func(d draft) bool { return drop[d.key] })
}

func parseText(syntax, text, locale string) (mf.Message, string, error) {
	var (
		m   mf.Message
		err error
	)
	if syntax == "mf2" {
		m, err = mf.ParseMF2(text)
	} else {
		m, err = mf.ParseMF1(text, locale)
	}
	if err != nil {
		return mf.Message{}, "", err
	}
	s, err := mf.Stringify(m)
	return m, s, err
}

func (g *generator) parseAll(drafts []draft) {
	for _, d := range drafts {
		if g.byKey[d.key] != nil {
			g.fail("duplicate key %s", d.key)
			continue
		}
		src, srcMF2, err := parseText(d.syntax, d.text[sourceLocale], sourceLocale)
		if err != nil {
			g.fail("%s de: %v", d.key, err)
			continue
		}
		it := &parsed{d: d, source: src, models: map[string]mf.Message{},
			msg: Message{Key: d.key, Namespace: d.ns, Pattern: d.pattern, Description: d.description, MaxLength: d.maxLength,
				Source: Text{Syntax: d.syntax, Text: d.text[sourceLocale], MF2: srcMF2}, Translations: map[string]*Translation{}}}
		for _, l := range targetLocales {
			text, ok := d.text[l]
			if !ok {
				continue
			}
			m, s, err := parseText(d.syntax, text, l)
			if err != nil {
				g.fail("%s %s: %v", d.key, l, err)
				continue
			}
			it.models[l] = m
			it.msg.Translations[l] = &Translation{Text: Text{Syntax: d.syntax, Text: text, MF2: s}}
		}
		g.items = append(g.items, it)
		g.byKey[d.key] = it
	}
}

func (g *generator) buildTermbase() {
	var cs []kdomain.Concept
	for _, c := range concepts {
		g.concepts[c.id] = c
		var terms []kdomain.TermInput
		for _, t := range conceptTerms(c) {
			terms = append(terms, kdomain.TermInput{Locale: t.Locale, Text: t.Text, Status: t.Status})
		}
		kc, err := kdomain.NewConcept(uuid.NewSHA1(uuid.NameSpaceURL, []byte("glossa-m2/"+c.id)), nil,
			kdomain.ConceptInput{Definition: c.definition, Domain: c.domain, Terms: terms}, "fixture", time.Unix(0, 0))
		if err != nil {
			g.fail("concept %s: %v", c.id, err)
			continue
		}
		cs = append(cs, kc)
	}
	g.termbase = kdomain.NewTermbase(cs)
}

// conceptTerms lists a concept's terms: preferred, admitted plurals,
// forbidden singular and plural.
func conceptTerms(c concept) []Term {
	var out []Term
	for _, l := range append([]string{sourceLocale}, targetLocales...) {
		f, ok := c.terms[l]
		if !ok {
			continue
		}
		out = append(out, Term{Locale: l, Text: f.pref, Status: "preferred"})
		if f.prefPl != "" && f.prefPl != f.pref {
			out = append(out, Term{Locale: l, Text: f.prefPl, Status: "admitted"})
		}
		if f.forb != "" {
			out = append(out, Term{Locale: l, Text: f.forb, Status: "forbidden"})
		}
		if f.forbPl != "" && f.forbPl != f.forb {
			out = append(out, Term{Locale: l, Text: f.forbPl, Status: "forbidden"})
		}
	}
	return out
}

func (g *generator) fixtureConcepts() []Concept {
	var out []Concept
	for _, c := range concepts {
		out = append(out, Concept{ID: c.id, Domain: c.domain, Definition: c.definition, Terms: conceptTerms(c)})
	}
	return out
}

func (g *generator) nonSensitive() (keys, sensitive []string) {
	for _, it := range g.items {
		if it.d.sensitive {
			sensitive = append(sensitive, it.d.key)
		} else {
			keys = append(keys, it.d.key)
		}
	}
	return keys, sensitive
}

func (g *generator) chooseExisting() {
	keys, sensitive := g.nonSensitive()
	for _, l := range targetLocales {
		chosen := pick(g.seed, "existing-"+l, keys, round(coverage[l]*float64(len(keys))))
		for k := range pick(g.seed, "existing-legal-"+l, sensitive, round(sensitiveCoverage[l]*float64(len(sensitive)))) {
			chosen[k] = true
		}
		for k := range chosen {
			if t := g.byKey[k].msg.Translations[l]; t != nil {
				t.Existing = true
			}
		}
	}
}

// norm is the translation-memory exact-match key of a message: the
// normalized text's hash and the placeholder signature.
func norm(m mf.Message) string {
	n := kdomain.Normalize(m)
	return n.Hash + "|" + n.Signature
}

// missing lists the non-sensitive messages without a translation in l.
func (g *generator) missing(l string) []*parsed {
	var out []*parsed
	for _, it := range g.items {
		if t := it.msg.Translations[l]; !it.d.sensitive && t != nil && !t.Existing {
			out = append(out, it)
		}
	}
	return out
}

func keysOf(items []*parsed) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.d.key
	}
	return out
}

// legacyMemory builds the tenant's legacy TM.
func (g *generator) legacyMemory() []LegacyUnit {
	var units []LegacyUnit
	sourceCount := map[string]int{}
	for _, it := range g.items {
		sourceCount[norm(it.source)]++
	}
	add := func(l, kind, source, target string) {
		units = append(units, LegacyUnit{ID: fmt.Sprintf("legacy-%s-%03d", l, len(units)+1),
			SourceLocale: sourceLocale, TargetLocale: l, Source: source, Target: target, Kind: kind})
	}
	for _, l := range fillLocales {
		approved := map[string]bool{}
		for _, it := range g.items {
			if t := it.msg.Translations[l]; t != nil && t.Existing {
				approved[norm(it.source)] = true
			}
		}
		var uncovered []*parsed
		for _, it := range g.missing(l) {
			if !approved[norm(it.source)] {
				uncovered = append(uncovered, it)
			}
		}
		used := map[string]bool{}
		// Legacy units with a forbidden term: on a unique source, so the
		// only exact match the agent finds is one it must not reuse.
		var forbiddenable []string
		for _, it := range uncovered {
			if n := it.d.noun; n != nil && n.concept != "" && g.concepts[n.concept].terms[l].forb != "" &&
				sourceCount[norm(it.source)] == 1 && it.d.pattern != "count" && it.d.pattern != "selected" {
				if _, changed := g.forbid(it, l); changed {
					forbiddenable = append(forbiddenable, it.d.key)
				}
			}
		}
		for _, k := range sortedKeys(pick(g.seed, "legacy-forbidden-"+l, forbiddenable, legacyForbidden[l])) {
			it := g.byKey[k]
			target, _ := g.forbid(it, l)
			add(l, "forbidden_term", it.msg.Source.MF2, target)
			used[k] = true
		}
		var exactable []string
		for _, it := range uncovered {
			if !used[it.d.key] {
				exactable = append(exactable, it.d.key)
			}
		}
		for _, k := range sortedKeys(pick(g.seed, "legacy-exact-"+l, exactable, legacyExact[l])) {
			it := g.byKey[k]
			add(l, "exact", it.msg.Source.MF2, it.msg.Translations[l].MF2)
			used[k] = true
		}
		var fuzzable []string
		for _, it := range uncovered {
			if !used[it.d.key] && it.d.pattern == "action" {
				fuzzable = append(fuzzable, it.d.key)
			}
		}
		for _, k := range sortedKeys(pick(g.seed, "legacy-fuzzy-"+l, fuzzable, legacyFuzzy[l])) {
			it := g.byKey[k]
			v := verbs[it.d.verb]
			src := it.d.noun.de.sg + " jetzt " + v.deInf
			add(l, "fuzzy", src, "今すぐ"+it.d.noun.ja.w+"を"+v.ja)
		}
	}
	return units
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// forbid replaces the noun's preferred term with its forbidden one in
// the reference translation.
func (g *generator) forbid(it *parsed, l string) (string, bool) {
	ref := it.msg.Translations[l].MF2
	f := g.concepts[it.d.noun.concept].terms[l]
	out := ref
	if f.prefPl != "" && f.forbPl != "" {
		out = strings.ReplaceAll(out, f.prefPl, f.forbPl)
	}
	out = strings.ReplaceAll(out, f.pref, f.forb)
	return out, out != ref
}

// route decides what a fill does with each missing translation of l and
// scripts the provider's slips.
func (g *generator) route(l string, legacy []LegacyUnit) LocaleExpectation {
	e := LocaleExpectation{Slips: map[string]int{}}
	clean, blocked := map[string]bool{}, map[string]bool{}
	for _, it := range g.items {
		if t := it.msg.Translations[l]; t != nil && t.Existing {
			clean[norm(it.source)] = true
		}
	}
	for _, u := range legacy {
		if u.TargetLocale != l || u.Kind == "fuzzy" {
			continue
		}
		m, err := mf.ParseMF2(u.Source)
		if err != nil {
			g.fail("legacy %s: %v", u.ID, err)
			continue
		}
		if u.Kind == "forbidden_term" {
			blocked[norm(m)] = true
		} else {
			clean[norm(m)] = true
		}
	}
	var pool []*parsed
	for _, it := range g.items {
		e.Messages++
		t := it.msg.Translations[l]
		switch {
		case t != nil && t.Existing:
			e.Existing++
			t.Route = RouteExisting
			continue
		case it.d.sensitive:
			e.Missing++
			e.Sensitive++
			if t != nil {
				t.Route = RouteSensitive
			}
			continue
		}
		e.Missing++
		k := norm(it.source)
		switch {
		case clean[k]:
			e.PreviewTMExact++
			e.TMReused++
			t.Route = RouteTM
		case blocked[k]:
			e.PreviewTMExact++
			e.TMBlocked++
			e.AIDrafted++
			t.Route, t.TMBlocked = RouteAI, true
		default:
			e.AIDrafted++
			t.Route = RouteAI
			pool = append(pool, it)
		}
	}
	g.slips(l, pool, &e)
	return e
}

func (g *generator) slips(l string, pool []*parsed, e *LocaleExpectation) {
	taken := map[string]bool{}
	assign := func(kind string, n int, eligible func(*parsed) bool) {
		var keys []string
		for _, it := range pool {
			if !taken[it.d.key] && eligible(it) {
				keys = append(keys, it.d.key)
			}
		}
		for _, k := range sortedKeys(pick(g.seed, "slip-"+kind+"-"+l, keys, n)) {
			taken[k] = true
			e.Slips[kind]++
			g.script(g.byKey[k], l, kind)
		}
	}
	count := func(rate float64) int { return max(1, round(rate*float64(len(pool)))) }
	for _, s := range slipRates {
		switch s.kind {
		case SlipForbiddenTerm:
			assign(s.kind, count(s.rate), func(it *parsed) bool {
				if n := it.d.noun; n == nil || n.concept == "" || g.concepts[n.concept].terms[l].forb == "" {
					return false
				}
				_, changed := g.forbid(it, l)
				return changed
			})
		case SlipFormality:
			assign(s.kind, count(s.rate), func(it *parsed) bool { return it.d.informal[l] != "" })
		case SlipTooLong:
			assign(s.kind, count(s.rate), func(it *parsed) bool { return it.d.maxLength > 0 && !it.source.IsSelect() })
		case SlipPlaceholderRepaired, SlipPlaceholderPersistent:
			assign(s.kind, count(s.rate), func(it *parsed) bool { return it.d.hasName })
		}
	}
	if l == "es" || l == "fr" {
		plural := 0
		for _, it := range pool {
			if it.d.noMany[l] != "" {
				plural++
			}
		}
		assign(SlipPluralMissing, max(1, round(pluralSlipShare*float64(plural))), func(it *parsed) bool { return it.d.noMany[l] != "" })
	}
}

// script writes the provider's answers for a slip.
func (g *generator) script(it *parsed, l, kind string) {
	t := it.msg.Translations[l]
	t.Slip = kind
	ref := t.MF2
	switch kind {
	case SlipForbiddenTerm:
		s, _ := g.forbid(it, l)
		t.Drafts = []Draft{{MF2: s}}
	case SlipFormality:
		_, s, err := parseText(it.d.syntax, it.d.informal[l], l)
		if err != nil {
			g.fail("%s %s informal: %v", it.d.key, l, err)
		}
		t.Drafts = []Draft{{MF2: s, Assessment: &Assessment{Score: 0.55, FormalityOK: false,
			Issues: []string{fmt.Sprintf("addresses the reader informally; the style guide asks for %q", formalPronoun[l])}}}}
	case SlipPluralMissing:
		_, s, err := parseText(it.d.syntax, it.d.noMany[l], l)
		if err != nil {
			g.fail("%s %s without many: %v", it.d.key, l, err)
		}
		t.Drafts = []Draft{{MF2: s}}
	case SlipTooLong:
		t.Drafts = []Draft{{MF2: ref + tooLongSuffix[l]}}
	case SlipPlaceholderRepaired:
		t.Drafts = []Draft{{MF2: dropName(ref)}, {MF2: ref}}
	case SlipPlaceholderPersistent:
		t.Drafts = []Draft{{MF2: dropName(ref)}}
	}
}

func dropName(mf2 string) string {
	return strings.TrimSpace(strings.Replace(mf2, "{$name}", "", 1))
}

// validate checks every reference translation and every scripted slip.
func (g *generator) validate() {
	scoring, policy := idomain.DefaultScoring(), idomain.DefaultReviewPolicy()
	de := bcp47.MustParse(sourceLocale)
	for _, it := range g.items {
		for _, l := range targetLocales {
			t := it.msg.Translations[l]
			if t == nil {
				continue
			}
			ref := it.models[l]
			if fs := mf.CheckCompat(it.source, ref, l); len(fs) > 0 {
				g.fail("%s %s: reference has structural findings %v", it.d.key, l, fs)
			}
			if it.d.maxLength > 0 && idomain.TextLength(ref) > it.d.maxLength {
				g.fail("%s %s: reference %q is longer than %d", it.d.key, l, t.MF2, it.d.maxLength)
			}
			if it.d.sensitive || l == "en" {
				continue
			}
			loc := bcp47.MustParse(l)
			if fs := kdomain.CheckTerminology(g.termbase, idomain.PlainText(it.source), de, idomain.PlainText(ref), loc); len(fs) > 0 {
				g.fail("%s %s: reference %q has terminology findings: %s", it.d.key, l, t.MF2, findingsText(fs))
			}
			self, ok := DefaultAssessment.Score, true
			c := scoring.Score(idomain.Signals{
				Origin: idomain.OriginAI, SelfAssessment: &self, FormalityOK: &ok,
				SourceLength: idomain.TextLength(it.source), TextLength: idomain.TextLength(ref), MaxLength: maxLen(it.d.maxLength),
				SourceLocale: sourceLocale, TargetLocale: l, MarkupCount: idomain.MarkupCount(it.source),
			})
			// The length heuristic compares the length ratio with one
			// expansion factor per language; natural translations of
			// sentence-length German text miss it now and then (short
			// labels are only judged by the severe band). Those are
			// flagged, not failed: the test expects them in the review
			// queue unless TM supports the draft.
			t.LengthFlagged = c.Has(idomain.FactorLengthRatio)
			if !t.LengthFlagged && policy.Route(c) != idomain.ActionApproveRecommended {
				g.fail("%s %s: reference %q scores %.3f %v", it.d.key, l, t.MF2, c.Score, c.Explanation)
			}
			g.validateSlip(it, l, t)
		}
	}
}

func (g *generator) validateSlip(it *parsed, l string, t *Translation) {
	if t.Slip == "" {
		return
	}
	first, err := mf.ParseMF2(t.Drafts[0].MF2)
	if err != nil {
		g.fail("%s %s: slip %s draft does not parse: %v", it.d.key, l, t.Slip, err)
		return
	}
	fs := mf.CheckCompat(it.source, first, l)
	errs := slices.ContainsFunc(fs, func(f mf.Finding) bool { return f.Severity == mf.SeverityError })
	loc := bcp47.MustParse(l)
	terms := kdomain.CheckTerminology(g.termbase, idomain.PlainText(it.source), bcp47.MustParse(sourceLocale), idomain.PlainText(first), loc)
	ok := true
	switch t.Slip {
	case SlipForbiddenTerm:
		ok = !errs && slices.ContainsFunc(terms, func(f kdomain.TermFinding) bool { return f.Code == kdomain.FindingTermForbidden })
	case SlipFormality:
		ok = !errs && len(terms) == 0
	case SlipPluralMissing:
		ok = !errs && slices.ContainsFunc(fs, func(f mf.Finding) bool { return f.Code == mf.FindingMissingPluralCategory })
	case SlipTooLong:
		ok = !errs && idomain.TextLength(first) > it.d.maxLength
	case SlipPlaceholderRepaired, SlipPlaceholderPersistent:
		ok = errs
	}
	if !ok {
		g.fail("%s %s: slip %s draft %q has no such effect (findings %v, terms %s)", it.d.key, l, t.Slip, t.Drafts[0].MF2, fs, findingsText(terms))
	}
}

func maxLen(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

func findingsText(fs []kdomain.TermFinding) string {
	var parts []string
	for _, f := range fs {
		parts = append(parts, fmt.Sprintf("%s %q", f.Code, f.Text))
	}
	return strings.Join(parts, "; ")
}

func styleGuides() []StyleGuide {
	yes := true
	return []StyleGuide{
		{Name: "Product voice", Fields: StyleFields{Tone: []string{"clear", "friendly", "concise"}},
			Rules: []StyleRule{{ID: "ui-buttons", Title: "Buttons name the action", Rationale: "People scan buttons for the verb.",
				Good: []string{"Download invoice"}, Bad: []string{"Click here"}}}},
		{Name: "Spanish (formal)", Locale: "es",
			Fields: StyleFields{Formality: &Formality{Register: "formal", Pronoun: "usted"}, Punctuation: &StylePunctuation{Quotes: "«»"}},
			Rules: []StyleRule{{ID: "es-questions", Title: "Open questions with ¿", Rationale: "Spanish marks questions at both ends.",
				Good: []string{"¿Seguro que desea continuar?"}, Bad: []string{"Seguro que desea continuar?"}}}},
		{Name: "French (formal)", Locale: "fr",
			Fields: StyleFields{Formality: &Formality{Register: "formal", Pronoun: "vous"}, Punctuation: &StylePunctuation{Quotes: "« »", SpaceBeforePunctuation: &yes}},
			Rules: []StyleRule{{ID: "fr-space", Title: "Put a space before ? ! : ;", Rationale: "French typography.",
				Good: []string{"Voulez-vous continuer ?"}, Bad: []string{"Voulez-vous continuer?"}}}},
		{Name: "Japanese (polite)", Locale: "ja",
			Fields: StyleFields{Formality: &Formality{Register: "formal", Pronoun: "です・ます"}},
			Rules: []StyleRule{{ID: "ja-polite", Title: "Use the polite です/ます form in sentences", Rationale: "Product copy addresses customers politely.",
				Good: []string{"請求書はまだありません。"}, Bad: []string{"請求書はまだない。"}}}},
		{Name: "Error messages", Namespace: "errors", Fields: StyleFields{Tone: []string{"calm", "blameless"}},
			Rules: []StyleRule{{ID: "errors-no-blame", Title: "Say what happened; never blame the reader", Rationale: "Errors are stressful.",
				Good: []string{"The invoice could not be downloaded."}, Bad: []string{"You broke the download."}}}},
	}
}
