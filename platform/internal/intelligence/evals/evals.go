// Package evals runs the translation agent against golden sets and
// measures it (RFC 0003 §4): structural pass rate, terminology and
// formality compliance, edit distance to reference translations, review
// routing and cost, per locale pair.
//
// Golden sets live in testdata/<source>-<target>/<case>.json. CI replays
// recorded provider answers from testdata/cassettes (deterministic and
// free); `go test -tags=live` records against real providers. A prompt,
// routing or model change must not regress the metrics in
// testdata/baseline.json.
package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/memory"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Case is one golden-set entry.
type Case struct {
	ID           string      `json:"id"`
	Description  string      `json:"description,omitempty"`
	SourceLocale string      `json:"source_locale"`
	TargetLocale string      `json:"target_locale"`
	Source       string      `json:"source"`
	Context      CaseContext `json:"context"`
	Knowledge    Knowledge   `json:"knowledge"`
	Expect       Expect      `json:"expect"`
	// References are acceptable translations in MF2 syntax.
	References []string `json:"references"`

	pair string
}

// Pair is "<source>-<target>".
func (c Case) Pair() string { return c.SourceLocale + "-" + c.TargetLocale }

// CaseContext is the message's context.
type CaseContext struct {
	Key         string   `json:"key"`
	Namespace   string   `json:"namespace,omitempty"`
	Description string   `json:"description,omitempty"`
	MaxLength   *int     `json:"max_length,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// Knowledge is the case's translation memory, termbase and style guide.
type Knowledge struct {
	TM    []memory.TMUnit    `json:"tm,omitempty"`
	Terms []memory.Concept   `json:"terms,omitempty"`
	Style *domain.StyleGuide `json:"style,omitempty"`
}

// Expect are the properties a good translation has.
type Expect struct {
	// Origin, when set, is the expected provenance ("ai" or
	// "translation_memory").
	Origin         string     `json:"origin,omitempty"`
	RequiredTerms  []string   `json:"required_terms,omitempty"`
	ForbiddenTerms []string   `json:"forbidden_terms,omitempty"`
	Formality      *Formality `json:"formality,omitempty"`
	// Action, when set, is the expected review routing
	// ("review_required" for a draft that stayed without the target's
	// plural categories).
	Action string `json:"action,omitempty"`
}

// Formality lists markers of the expected form of address.
type Formality struct {
	MustIncludeAny []string `json:"must_include_any,omitempty"`
	MustNotInclude []string `json:"must_not_include,omitempty"`
}

// LoadCases reads every golden set under dir (dir/<pair>/*.json).
func LoadCases(dir string) ([]Case, error) {
	var cases []Case
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// "linguistic" holds the linguistic layer's own golden set, which
		// LoadReviewCases reads; it is not a translation case.
		if d.IsDir() && path != dir && (d.Name() == "cassettes" || d.Name() == "scripted" || d.Name() == "linguistic") {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".json" || filepath.Dir(path) == dir {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var c Case
		if err := json.Unmarshal(raw, &c); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		c.pair = filepath.Base(filepath.Dir(path))
		if c.pair != c.Pair() || c.ID != strings.TrimSuffix(filepath.Base(path), ".json") {
			return fmt.Errorf("%s: id %q and locales %s must match the file's name and directory", path, c.ID, c.Pair())
		}
		cases = append(cases, c)
		return nil
	})
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, err
}

// Tenant is the eval tenant.
const Tenant = "eval"

// Request is the translation request of the case.
func (c Case) Request() domain.TranslationRequest {
	return domain.TranslationRequest{
		Scope: domain.Scope{TenantID: Tenant, ProjectID: "eval"}, MessageID: c.ID, Key: c.Context.Key,
		Namespace: c.Context.Namespace, SourceLocale: c.SourceLocale, TargetLocale: c.TargetLocale,
		Source: c.Source, Tags: c.Context.Tags, ProviderConsent: true,
	}
}

// KnowledgeStore builds the case's in-memory knowledge.
func (c Case) KnowledgeStore() *memory.Knowledge {
	k := memory.NewKnowledge(Tenant)
	k.AddUnits(c.Knowledge.TM...)
	k.AddConcepts(c.Knowledge.Terms...)
	if c.Knowledge.Style != nil {
		k.SetStyle(c.TargetLocale, *c.Knowledge.Style)
	}
	k.SetContext(domain.MessageContext{
		MessageID: c.ID, Key: c.Context.Key, Namespace: c.Context.Namespace,
		Description: c.Context.Description, MaxLength: c.Context.MaxLength, Tags: c.Context.Tags,
	})
	return k
}

// Setup gives the providers and routing a case runs with.
type Setup struct {
	Providers map[string]domain.Provider
	Routing   domain.RoutingPolicy
	Prices    domain.PriceTable
}

// Outcome is one case's result and measurements.
type Outcome struct {
	Case       string            `json:"case"`
	Pair       string            `json:"pair"`
	Error      string            `json:"error,omitempty"`
	Suggestion domain.Suggestion `json:"-"`
	Message    string            `json:"message,omitempty"`
	Structural bool              `json:"structural"`
	// Terminology and Formality are nil when the case expects nothing.
	Terminology *bool `json:"terminology,omitempty"`
	Formality   *bool `json:"formality,omitempty"`
	OriginOK    bool  `json:"origin_ok"`
	// ActionOK is false when the case expects another review routing.
	ActionOK   bool    `json:"action_ok"`
	EditRatio  float64 `json:"edit_ratio"`
	Exact      bool    `json:"exact"`
	Confidence float64 `json:"confidence"`
	Action     string  `json:"action,omitempty"`
	Repairs    int     `json:"repairs"`
	Calls      int     `json:"calls"`
	CostMicros int64   `json:"cost_micro_usd"`
}

// RunCase translates one case and measures the result. Only an
// infrastructure problem (a cassette miss) is returned as an error; a
// failed translation is a measured outcome.
func RunCase(ctx context.Context, c Case, setup Setup) (Outcome, error) {
	router := app.NewRouter(setup.Providers, setup.Routing, setup.Prices, domain.NoBudget{})
	tr, err := app.NewTranslator(app.Config{Router: router, Knowledge: c.KnowledgeStore()})
	if err != nil {
		return Outcome{}, err
	}
	res, err := tr.Translate(ctx, c.Request())
	o := Outcome{Case: c.ID, Pair: c.Pair(), EditRatio: 1}
	if err != nil {
		if errors.Is(err, cassette.ErrMismatch) {
			return Outcome{}, err
		}
		o.Error = err.Error()
		// A failed job still spent money on the calls it made.
		for _, a := range res.Audit {
			if a.Tool == app.ToolDraft || a.Tool == app.ToolAssess {
				var call struct {
					Cost int64 `json:"cost"`
				}
				if json.Unmarshal(a.Output, &call) == nil {
					o.Calls++
					o.CostMicros += call.Cost
				}
			}
		}
		return o, nil
	}
	s := res.Suggestion
	o.Suggestion, o.Message = s, s.Message
	o.Structural = true
	o.OriginOK = c.Expect.Origin == "" || c.Expect.Origin == string(s.Provenance.Origin)
	o.ActionOK = c.Expect.Action == "" || c.Expect.Action == string(s.Action)
	o.Confidence, o.Action, o.Repairs = s.Confidence.Score, string(s.Action), s.Provenance.Repairs
	o.Calls, o.CostMicros = len(s.Calls), int64(s.Cost)
	text := domain.PlainText(s.Model)
	if len(c.Expect.RequiredTerms)+len(c.Expect.ForbiddenTerms) > 0 {
		ok := true
		for _, t := range c.Expect.RequiredTerms {
			ok = ok && containsFold(text, t)
		}
		for _, t := range c.Expect.ForbiddenTerms {
			ok = ok && !containsWord(text, t, c.TargetLocale)
		}
		o.Terminology = &ok
	}
	if f := c.Expect.Formality; f != nil {
		ok := len(f.MustIncludeAny) == 0
		for _, m := range f.MustIncludeAny {
			ok = ok || containsWord(text, m, c.TargetLocale)
		}
		for _, m := range f.MustNotInclude {
			ok = ok && !containsWord(text, m, c.TargetLocale)
		}
		o.Formality = &ok
	}
	for _, ref := range c.References {
		canonical := ref
		if _, cr, err := domain.ParseMessage(ref); err == nil {
			canonical = cr
		}
		ratio := editRatio(s.Message, canonical)
		if ratio < o.EditRatio {
			o.EditRatio = ratio
		}
	}
	o.Exact = o.EditRatio == 0
	return o, nil
}

// PairReport aggregates a pair (or everything, as "all").
type PairReport struct {
	Pair  string `json:"pair"`
	Cases int    `json:"cases"`
	// StructuralPassRate: suggestions that passed the structural gate.
	StructuralPassRate float64 `json:"structural_pass_rate"`
	// TerminologyCompliance and FormalityCompliance: over the cases
	// that expect terms / a form of address.
	TerminologyCompliance float64 `json:"terminology_compliance"`
	FormalityCompliance   float64 `json:"formality_compliance"`
	// MeanEditRatio is the mean normalized character edit distance to the
	// closest reference (0 = identical); ExactRate the share identical.
	MeanEditRatio  float64        `json:"mean_edit_ratio"`
	ExactRate      float64        `json:"exact_rate"`
	OriginAccuracy float64        `json:"origin_accuracy"`
	MeanConfidence float64        `json:"mean_confidence"`
	Actions        map[string]int `json:"actions"`
	Repairs        int            `json:"repairs"`
	Calls          int            `json:"calls"`
	CostMicroUSD   int64          `json:"cost_micro_usd"`
}

// Report is a whole eval run.
type Report struct {
	Pairs    []PairReport `json:"pairs"`
	Overall  PairReport   `json:"overall"`
	Outcomes []Outcome    `json:"outcomes"`
}

// Summarize aggregates outcomes per pair and overall.
func Summarize(outcomes []Outcome) Report {
	byPair := map[string][]Outcome{}
	for _, o := range outcomes {
		byPair[o.Pair] = append(byPair[o.Pair], o)
	}
	r := Report{Outcomes: outcomes, Overall: aggregate("all", outcomes)}
	for _, p := range slices.Sorted(maps.Keys(byPair)) {
		r.Pairs = append(r.Pairs, aggregate(p, byPair[p]))
	}
	return r
}

func aggregate(pair string, os []Outcome) PairReport {
	r := PairReport{Pair: pair, Cases: len(os), Actions: map[string]int{}}
	var structural, terms, termCases, formal, formalCases, exact, origin int
	var edit, conf float64
	for _, o := range os {
		if o.Structural {
			structural++
			conf += o.Confidence
			r.Actions[o.Action]++
		} else {
			r.Actions["failed"]++
		}
		if o.Terminology != nil {
			termCases++
			if *o.Terminology {
				terms++
			}
		}
		if o.Formality != nil {
			formalCases++
			if *o.Formality {
				formal++
			}
		}
		if o.Exact {
			exact++
		}
		if o.OriginOK && o.Structural {
			origin++
		}
		edit += o.EditRatio
		r.Repairs += o.Repairs
		r.Calls += o.Calls
		r.CostMicroUSD += o.CostMicros
	}
	n := float64(max(len(os), 1))
	r.StructuralPassRate = round(float64(structural) / n)
	r.TerminologyCompliance = rate(terms, termCases)
	r.FormalityCompliance = rate(formal, formalCases)
	r.MeanEditRatio = round(edit / n)
	r.ExactRate = round(float64(exact) / n)
	r.OriginAccuracy = round(float64(origin) / n)
	if structural > 0 {
		r.MeanConfidence = round(conf / float64(structural))
	}
	return r
}

// String renders the report as a table.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-7s %5s %10s %6s %6s %6s %6s %6s %8s %5s %5s %10s  %s\n",
		"pair", "cases", "structural", "terms", "formal", "edit", "exact", "conf", "repairs", "calls", "", "cost", "actions")
	for _, p := range append(slices.Clone(r.Pairs), r.Overall) {
		fmt.Fprintf(&b, "%-7s %5d %10.2f %6.2f %6.2f %6.3f %6.2f %6.3f %8d %5d %5s %10s  %s\n",
			p.Pair, p.Cases, p.StructuralPassRate, p.TerminologyCompliance, p.FormalityCompliance,
			p.MeanEditRatio, p.ExactRate, p.MeanConfidence, p.Repairs, p.Calls, "",
			domain.MicroUSD(p.CostMicroUSD).String(), actions(p.Actions))
	}
	return b.String()
}

// Baseline holds the tracked metrics per pair: the rates are minimums,
// the edit ratio a maximum.
type Baseline map[string]BaselineEntry

// BaselineEntry is one pair's tracked metrics.
type BaselineEntry struct {
	Cases                 int     `json:"cases"`
	StructuralPassRate    float64 `json:"structural_pass_rate"`
	TerminologyCompliance float64 `json:"terminology_compliance"`
	FormalityCompliance   float64 `json:"formality_compliance"`
	MeanEditRatio         float64 `json:"mean_edit_ratio"`
	OriginAccuracy        float64 `json:"origin_accuracy"`
}

// Regressions compares r with the baseline: rates may not drop, the edit
// ratio may not rise (beyond a small tolerance).
func (r Report) Regressions(base Baseline) []string {
	const tol = 1e-6
	var out []string
	for _, p := range append(slices.Clone(r.Pairs), r.Overall) {
		b, ok := base[p.Pair]
		if !ok {
			out = append(out, fmt.Sprintf("%s: no baseline (update it with -update-baseline)", p.Pair))
			continue
		}
		check := func(name string, got, min float64) {
			if got < min-tol {
				out = append(out, fmt.Sprintf("%s: %s %.3f < baseline %.3f", p.Pair, name, got, min))
			}
		}
		check("structural_pass_rate", p.StructuralPassRate, b.StructuralPassRate)
		check("terminology_compliance", p.TerminologyCompliance, b.TerminologyCompliance)
		check("formality_compliance", p.FormalityCompliance, b.FormalityCompliance)
		check("origin_accuracy", p.OriginAccuracy, b.OriginAccuracy)
		if p.MeanEditRatio > b.MeanEditRatio+tol {
			out = append(out, fmt.Sprintf("%s: mean_edit_ratio %.3f > baseline %.3f", p.Pair, p.MeanEditRatio, b.MeanEditRatio))
		}
	}
	return out
}

// BaselineOf turns a report into a baseline.
func BaselineOf(r Report) Baseline {
	b := Baseline{}
	for _, p := range append(slices.Clone(r.Pairs), r.Overall) {
		b[p.Pair] = BaselineEntry{
			Cases: p.Cases, StructuralPassRate: p.StructuralPassRate,
			TerminologyCompliance: p.TerminologyCompliance, FormalityCompliance: p.FormalityCompliance,
			MeanEditRatio: p.MeanEditRatio, OriginAccuracy: p.OriginAccuracy,
		}
	}
	return b
}

// editRatio is the Levenshtein distance over runes divided by the longer
// length: 0 identical, 1 nothing in common.
func editRatio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return round(float64(prev[len(rb)]) / float64(max(len(ra), len(rb))))
}

func containsFold(text, s string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(s))
}

// containsWord matches whole words (case-sensitive: "Sie" is not "sie"),
// or substrings for languages written without spaces.
func containsWord(text, w, locale string) bool {
	lang, _, _ := strings.Cut(locale, "-")
	if lang == "ja" || lang == "zh" || lang == "ko" {
		return strings.Contains(text, w)
	}
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '\'' })
	wanted := strings.FieldsFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '\'' })
	for i := 0; i+len(wanted) <= len(words) && len(wanted) > 0; i++ {
		if slices.Equal(words[i:i+len(wanted)], wanted) {
			return true
		}
	}
	return false
}

func rate(n, of int) float64 {
	if of == 0 {
		return 1
	}
	return round(float64(n) / float64(of))
}

func round(v float64) float64 { return float64(int64(v*1000+0.5)) / 1000 }

func actions(m map[string]int) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}
