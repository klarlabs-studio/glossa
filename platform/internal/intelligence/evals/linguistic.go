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

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/linguistic"
	qdomain "go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The linguistic layer's golden set (RFC 0005 §3.8, RFC 0003 §4).
//
// It lives beside the translation agent's because it is the same
// machinery measured the same way: cases in
// testdata/linguistic/<pair>/*.json, recorded provider answers in
// testdata/linguistic/cassettes, tracked numbers in
// testdata/linguistic/baseline.json, and a prompt or model change that
// moves them has to move them in the right direction on purpose.
//
// What is measured is different, because the job is different. A
// translation is graded on what it produced; a reviewer is graded on
// what it noticed and on what it imagined. So the numbers are: how
// often the answer was the structure at all, how much of what a case
// plants the model finds, how often it invents a problem in a
// translation that has none, and how much of what it reports nobody
// asked about.
//
// The eval runs the shipped path — the Quality context's adapter over
// the Intelligence router — so what it measures is the layer, prompt,
// schema, parser and sealing included, and not a copy of it that could
// drift.

// ReviewCase is one linguistic golden-set entry.
type ReviewCase struct {
	ID           string `json:"id"`
	Description  string `json:"description,omitempty"`
	SourceLocale string `json:"source_locale"`
	TargetLocale string `json:"target_locale"`
	// Source and Translation are MF2 syntax.
	Source      string      `json:"source"`
	Translation string      `json:"translation"`
	Context     CaseContext `json:"context"`
	// Style is the effective style guide, where the case has one. The
	// prose rules matter here: RFC 0005 §3.2 keeps them out of the
	// mechanical style layer precisely so they are evidence for this one.
	Style *domain.StyleGuide `json:"style,omitempty"`
	// Terms is the glossary shown to the reviewer as evidence.
	Terms  []domain.TermHit `json:"terms,omitempty"`
	Expect ReviewExpect     `json:"expect"`

	pair string
}

// Pair is "<source>-<target>".
func (c ReviewCase) Pair() string { return c.SourceLocale + "-" + c.TargetLocale }

// ReviewExpect is what a good review of the case says.
type ReviewExpect struct {
	// Codes are the codes a competent reviewer reports. An empty list is
	// a clean case: the translation is sound and a finding on it is a
	// false alarm.
	Codes []string `json:"codes"`
	// Quotes, where given, are texts the findings must between them
	// point at, so a case is not satisfied by the right code on the
	// wrong words.
	Quotes []string `json:"quotes,omitempty"`
}

// Clean reports whether the case expects nothing.
func (e ReviewExpect) Clean() bool { return len(e.Codes) == 0 }

// LoadReviewCases reads every linguistic golden set under dir
// (dir/<pair>/*.json).
func LoadReviewCases(dir string) ([]ReviewCase, error) {
	var cases []ReviewCase
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir && (d.Name() == "cassettes" || d.Name() == "scripted") {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(path) != ".json" || filepath.Dir(path) == dir {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var c ReviewCase
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

// Request is the review request of the case, as the linguistic-QA job
// would build it.
func (c ReviewCase) Request() linguistic.Request {
	return linguistic.Request{
		Tenant: Tenant, Project: "eval",
		Message: c.ID, Key: c.Context.Key, Namespace: c.Context.Namespace,
		Revision: "r_eval", SourceLocale: c.SourceLocale, TargetLocale: c.TargetLocale,
		Source: c.Source, Translation: c.Translation, Description: c.Context.Description,
		Terms: c.Terms, Style: c.Style, ProviderConsent: true,
	}
}

// ReviewOutcome is one case's result and measurements.
type ReviewOutcome struct {
	Case  string `json:"case"`
	Pair  string `json:"pair"`
	Error string `json:"error,omitempty"`
	// Accepted: the answer was the constrained structure and survived
	// parsing. A refused answer is a measured outcome, not a test error.
	Accepted bool `json:"accepted"`
	// Reported are the codes the review reported, deduplicated.
	Reported []string `json:"reported,omitempty"`
	Expected []string `json:"expected,omitempty"`
	// Found are the expected codes that were reported; Spurious the
	// reported codes nobody asked about.
	Found    int `json:"found"`
	Spurious int `json:"spurious"`
	Findings int `json:"findings"`
	// QuotesOK is false when a case named quotes the findings missed.
	QuotesOK bool `json:"quotes_ok"`
	// Advisory is false if any finding came out as anything but a
	// warning — which would be a bug in the layer, not a model's fault.
	Advisory bool `json:"advisory"`
	// SpansOK is false if a finding's span does not cover its subject in
	// the text it names.
	SpansOK    bool  `json:"spans_ok"`
	CostMicros int64 `json:"cost_micro_usd"`
}

// RunReviewCase reviews one case and measures the result. Only an
// infrastructure problem (a cassette miss) is returned as an error; a
// refused answer is a measured outcome.
func RunReviewCase(ctx context.Context, c ReviewCase, setup Setup) (ReviewOutcome, error) {
	router := app.NewRouter(setup.Providers, setup.Routing, setup.Prices, domain.NoBudget{})
	rev, err := linguistic.New(router)
	if err != nil {
		return ReviewOutcome{}, err
	}
	res, err := rev.Review(ctx, c.Request())
	o := ReviewOutcome{
		Case: c.ID, Pair: c.Pair(), Expected: c.Expect.Codes,
		Advisory: true, SpansOK: true, QuotesOK: true, CostMicros: int64(res.Cost),
	}
	switch {
	case errors.Is(err, cassette.ErrMismatch):
		return ReviewOutcome{}, err
	case err != nil:
		o.Error = err.Error()
		o.QuotesOK = c.Expect.Clean()
		return o, nil
	}
	o.Accepted = true
	fs := res.Findings()
	o.Findings = len(fs)
	for _, f := range fs {
		if f.Severity != qdomain.Warning {
			o.Advisory = false
		}
		if !slices.Contains(o.Reported, f.Code) {
			o.Reported = append(o.Reported, f.Code)
		}
	}
	sort.Strings(o.Reported)
	o.SpansOK = spansCover(fs, res.Reviewed)
	for _, want := range c.Expect.Codes {
		if slices.Contains(o.Reported, want) {
			o.Found++
		}
	}
	for _, got := range o.Reported {
		if !slices.Contains(c.Expect.Codes, got) {
			o.Spurious++
		}
	}
	for _, q := range c.Expect.Quotes {
		if !quoted(fs, q) {
			o.QuotesOK = false
		}
	}
	return o, nil
}

// spansCover checks the findings point at the words they name: the
// span the reviewer resolved must delimit exactly the quote that became
// the finding's subject. It is the layer's own invariant, measured over
// the whole golden set rather than asserted once.
func spansCover(fs []qdomain.Finding, reviewed []layers.Reviewed) bool {
	if len(fs) > len(reviewed) {
		return false
	}
	for _, r := range reviewed {
		s := r.Locus.Span
		if s == nil || s.Start < 0 || s.End-s.Start != len(r.Quote) {
			return false
		}
	}
	return true
}

func quoted(fs []qdomain.Finding, want string) bool {
	for _, f := range fs {
		if strings.Contains(f.Subject, want) || strings.Contains(want, f.Subject) {
			return true
		}
	}
	return false
}

// ReviewPairReport aggregates a pair (or everything, as "all").
type ReviewPairReport struct {
	Pair  string `json:"pair"`
	Cases int    `json:"cases"`
	// AcceptRate: answers that were the constrained structure.
	AcceptRate float64 `json:"accept_rate"`
	// Detection: of the codes the cases plant, the share reported.
	Detection float64 `json:"detection"`
	// FalseAlarmRate: of the clean cases, the share that got a finding.
	FalseAlarmRate float64 `json:"false_alarm_rate"`
	// SpuriousRate: of every code reported, the share nobody asked for.
	SpuriousRate float64 `json:"spurious_rate"`
	// QuoteAccuracy: of the cases that name quotes, the share whose
	// findings point at them.
	QuoteAccuracy float64 `json:"quote_accuracy"`
	// Advisory and SpansOK are invariants, not scores: they are 1 or the
	// layer is broken.
	Advisory     float64 `json:"advisory"`
	SpansOK      float64 `json:"spans_ok"`
	Findings     int     `json:"findings"`
	CleanCases   int     `json:"clean_cases"`
	CostMicroUSD int64   `json:"cost_micro_usd"`
}

// ReviewReport is a whole linguistic eval run.
type ReviewReport struct {
	Pairs    []ReviewPairReport `json:"pairs"`
	Overall  ReviewPairReport   `json:"overall"`
	Outcomes []ReviewOutcome    `json:"outcomes"`
}

// SummarizeReviews aggregates outcomes per pair and overall.
func SummarizeReviews(outcomes []ReviewOutcome) ReviewReport {
	byPair := map[string][]ReviewOutcome{}
	for _, o := range outcomes {
		byPair[o.Pair] = append(byPair[o.Pair], o)
	}
	r := ReviewReport{Outcomes: outcomes, Overall: aggregateReviews("all", outcomes)}
	for _, p := range slices.Sorted(maps.Keys(byPair)) {
		r.Pairs = append(r.Pairs, aggregateReviews(p, byPair[p]))
	}
	return r
}

func aggregateReviews(pair string, os []ReviewOutcome) ReviewPairReport {
	r := ReviewPairReport{Pair: pair, Cases: len(os)}
	var accepted, expected, found, reported, spurious int
	var falseAlarms, quoteCases, quoteOK, advisory, spans int
	for _, o := range os {
		if o.Accepted {
			accepted++
		}
		if o.Advisory {
			advisory++
		}
		if o.SpansOK {
			spans++
		}
		expected += len(o.Expected)
		found += o.Found
		reported += len(o.Reported)
		spurious += o.Spurious
		r.Findings += o.Findings
		r.CostMicroUSD += o.CostMicros
		if len(o.Expected) == 0 {
			r.CleanCases++
			if o.Findings > 0 {
				falseAlarms++
			}
		}
		if len(o.Expected) > 0 {
			quoteCases++
			if o.QuotesOK {
				quoteOK++
			}
		}
	}
	n := max(len(os), 1)
	r.AcceptRate = rate(accepted, n)
	r.Advisory = rate(advisory, n)
	r.SpansOK = rate(spans, n)
	r.Detection = rate(found, expected)
	r.FalseAlarmRate = round(float64(falseAlarms) / float64(max(r.CleanCases, 1)))
	if r.CleanCases == 0 {
		r.FalseAlarmRate = 0
	}
	r.SpuriousRate = round(float64(spurious) / float64(max(reported, 1)))
	if reported == 0 {
		r.SpuriousRate = 0
	}
	r.QuoteAccuracy = rate(quoteOK, quoteCases)
	return r
}

// String renders the report as a table.
func (r ReviewReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-7s %5s %7s %7s %11s %9s %9s %9s %9s %10s\n",
		"pair", "cases", "accept", "detect", "false-alarm", "spurious", "quotes", "advisory", "spans", "cost")
	for _, p := range append(slices.Clone(r.Pairs), r.Overall) {
		fmt.Fprintf(&b, "%-7s %5d %7.2f %7.2f %11.2f %9.2f %9.2f %9.2f %9.2f %10s\n",
			p.Pair, p.Cases, p.AcceptRate, p.Detection, p.FalseAlarmRate, p.SpuriousRate,
			p.QuoteAccuracy, p.Advisory, p.SpansOK, domain.MicroUSD(p.CostMicroUSD).String())
	}
	return b.String()
}

// ReviewBaseline holds the tracked numbers per pair.
type ReviewBaseline map[string]ReviewBaselineEntry

// ReviewBaselineEntry is one pair's tracked numbers: the first four are
// minimums, the last two maximums.
type ReviewBaselineEntry struct {
	Cases          int     `json:"cases"`
	AcceptRate     float64 `json:"accept_rate"`
	Detection      float64 `json:"detection"`
	QuoteAccuracy  float64 `json:"quote_accuracy"`
	Advisory       float64 `json:"advisory"`
	SpansOK        float64 `json:"spans_ok"`
	FalseAlarmRate float64 `json:"false_alarm_rate"`
	SpuriousRate   float64 `json:"spurious_rate"`
}

// Regressions compares r with the baseline.
func (r ReviewReport) Regressions(base ReviewBaseline) []string {
	const tol = 1e-6
	var out []string
	for _, p := range append(slices.Clone(r.Pairs), r.Overall) {
		b, ok := base[p.Pair]
		if !ok {
			out = append(out, fmt.Sprintf("%s: no baseline (update it with -update-baseline)", p.Pair))
			continue
		}
		atLeast := func(name string, got, min float64) {
			if got < min-tol {
				out = append(out, fmt.Sprintf("%s: %s %.3f < baseline %.3f", p.Pair, name, got, min))
			}
		}
		atMost := func(name string, got, max float64) {
			if got > max+tol {
				out = append(out, fmt.Sprintf("%s: %s %.3f > baseline %.3f", p.Pair, name, got, max))
			}
		}
		atLeast("accept_rate", p.AcceptRate, b.AcceptRate)
		atLeast("detection", p.Detection, b.Detection)
		atLeast("quote_accuracy", p.QuoteAccuracy, b.QuoteAccuracy)
		atLeast("advisory", p.Advisory, b.Advisory)
		atLeast("spans_ok", p.SpansOK, b.SpansOK)
		atMost("false_alarm_rate", p.FalseAlarmRate, b.FalseAlarmRate)
		atMost("spurious_rate", p.SpuriousRate, b.SpuriousRate)
	}
	return out
}

// ReviewBaselineOf turns a report into a baseline.
func ReviewBaselineOf(r ReviewReport) ReviewBaseline {
	b := ReviewBaseline{}
	for _, p := range append(slices.Clone(r.Pairs), r.Overall) {
		b[p.Pair] = ReviewBaselineEntry{
			Cases: p.Cases, AcceptRate: p.AcceptRate, Detection: p.Detection,
			QuoteAccuracy: p.QuoteAccuracy, Advisory: p.Advisory, SpansOK: p.SpansOK,
			FalseAlarmRate: p.FalseAlarmRate, SpuriousRate: p.SpuriousRate,
		}
	}
	return b
}
