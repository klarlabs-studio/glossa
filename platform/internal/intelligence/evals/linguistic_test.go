package evals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/evals"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The linguistic layer's evals (RFC 0005 §3.8). They run the shipped
// path — the Quality adapter over the Intelligence router — against
// recorded answers only. There is no provider in this file and no
// network call anywhere in it: the only Provider the router is given is
// the one the cassette plays back, and a request the cassette has no
// recording of fails with cassette.ErrMismatch rather than reaching
// anything.

const linguisticData = "testdata/linguistic"

func loadReviewCases(t *testing.T) []evals.ReviewCase {
	t.Helper()
	cases, err := evals.LoadReviewCases(linguisticData)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no linguistic golden cases")
	}
	return cases
}

func reviewCassettePath(c evals.ReviewCase) string {
	return filepath.Join(linguisticData, "cassettes", c.Pair(), c.ID+".json")
}

// The golden set itself must be right: every source and translation is
// valid MF2, every expected code is one the layer knows, and every
// expected quote is somewhere in the texts.
func TestLinguisticGoldenSetIsValid(t *testing.T) {
	cases := loadReviewCases(t)
	pairs := map[string]int{}
	clean := 0
	for _, c := range cases {
		pairs[c.Pair()]++
		if c.Expect.Clean() {
			clean++
		}
		for name, src := range map[string]string{"source": c.Source, "translation": c.Translation} {
			if _, err := mf.ParseMF2(src); err != nil {
				t.Errorf("%s: %s: %v", c.ID, name, err)
			}
		}
		for _, code := range c.Expect.Codes {
			if !layers.KnownLinguisticCode(code) {
				t.Errorf("%s: expects %q, which is not one of the layer's codes", c.ID, code)
			}
		}
		for _, q := range c.Expect.Quotes {
			if !strings.Contains(c.Source, q) && !strings.Contains(c.Translation, q) {
				t.Errorf("%s: expected quote %q is in neither text", c.ID, q)
			}
		}
	}
	for _, want := range []string{"en-de", "de-fr", "en-ja"} {
		if pairs[want] < 6 {
			t.Errorf("pair %s has %d cases, want at least 6", want, pairs[want])
		}
	}
	// A detector measured only on cases that plant a problem measures
	// nothing about how often it invents one.
	if clean < len(cases)/4 {
		t.Errorf("%d of %d cases are clean; a false-alarm rate needs sound translations to be wrong about", clean, len(cases))
	}
	for _, c := range layers.LinguisticCodes {
		found := false
		for _, cs := range cases {
			for _, code := range cs.Expect.Codes {
				found = found || code == c.Code
			}
		}
		if !found {
			t.Errorf("no golden case plants %q", c.Code)
		}
	}
}

// TestLinguisticEvalsOnCassettes is the CI eval: the layer runs over
// every golden case against recorded answers and must not regress the
// baseline.
func TestLinguisticEvalsOnCassettes(t *testing.T) {
	ctx := context.Background()
	var outcomes []evals.ReviewOutcome
	for _, c := range loadReviewCases(t) {
		cas, err := cassette.Load(reviewCassettePath(c))
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		o, err := evals.RunReviewCase(ctx, c, evals.Setup{
			Providers: map[string]domain.Provider{"anthropic": cas.Provider("anthropic")},
			Routing:   app.DefaultRouting(),
			Prices:    app.DefaultPrices(),
		})
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if unused := cas.Unused(); len(unused) != 0 {
			t.Errorf("%s: %d recorded interactions were not replayed; re-record the cassette", c.ID, len(unused))
		}
		// These two are the layer's own invariants and not a model's
		// score: a finding that is not a warning, or a span that does not
		// delimit its own quote, is a bug here.
		if !o.Advisory {
			t.Errorf("%s: a finding came out as something other than a warning", c.ID)
		}
		if !o.SpansOK {
			t.Errorf("%s: a finding's span does not delimit its quote", c.ID)
		}
		outcomes = append(outcomes, o)
	}
	report := evals.SummarizeReviews(outcomes)
	t.Logf("linguistic eval report (cassettes):\n%s", report)
	for _, o := range report.Outcomes {
		if o.Found < len(o.Expected) || o.Spurious > 0 || !o.Accepted || !o.QuotesOK {
			t.Logf("  %-28s accepted=%v expected=%v reported=%v quotes=%v %s",
				o.Case, o.Accepted, o.Expected, o.Reported, o.QuotesOK, o.Error)
		}
	}

	path := filepath.Join(linguisticData, "baseline.json")
	if *updateBaseline {
		raw, _ := json.MarshalIndent(evals.ReviewBaselineOf(report), "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("baseline: %v (create it with -update-baseline)", err)
	}
	var base evals.ReviewBaseline
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	if regressions := report.Regressions(base); len(regressions) > 0 {
		t.Errorf("linguistic eval regressions against %s:\n  %s", path, strings.Join(regressions, "\n  "))
	}
}

// A cassette that no longer matches the prompt fails loudly, with what
// changed and how to re-record — the same contract the translation
// evals have, and the reason a prompt edit cannot quietly invalidate
// the numbers.
func TestLinguisticCassetteMismatchFailsLoudly(t *testing.T) {
	var c evals.ReviewCase
	for _, x := range loadReviewCases(t) {
		if x.ID == "en-de-pay-tone" {
			c = x
		}
	}
	cas, err := cassette.Load(reviewCassettePath(c))
	if err != nil {
		t.Fatal(err)
	}
	c.Context.Description = "A different description changes the prompt"
	_, err = evals.RunReviewCase(context.Background(), c, evals.Setup{
		Providers: map[string]domain.Provider{"anthropic": cas.Provider("anthropic")},
		Routing:   app.DefaultRouting(), Prices: app.DefaultPrices(),
	})
	if err == nil || !strings.Contains(err.Error(), "description: A different description") || !strings.Contains(err.Error(), "TestRecord.*Cassettes") {
		t.Fatalf("err = %v", err)
	}
}

// TestRecordLinguisticCassettes rewrites testdata/linguistic/cassettes.
// With -record=scripted the "model" answers come from
// testdata/linguistic/scripted/<pair>.json — the hand-crafted answers
// CI replays. With -tags=live -record=live they come from the real
// provider. Review the diff: the recorded prompts show what changed.
func TestRecordLinguisticCassettes(t *testing.T) {
	if *record == "" {
		t.Skip("set -record=scripted or -record=live to re-record cassettes")
	}
	var outcomes []evals.ReviewOutcome
	for _, c := range loadReviewCases(t) {
		var setup evals.Setup
		switch *record {
		case "scripted":
			answers, err := scriptedReviewAnswers(c)
			if err != nil {
				t.Fatal(err)
			}
			setup = evals.Setup{
				Providers: map[string]domain.Provider{"anthropic": &scriptedProvider{answers: answers}},
				Routing:   app.DefaultRouting(), Prices: app.DefaultPrices(),
			}
		case "live":
			if liveSetup == nil {
				t.Fatal("-record=live needs -tags=live")
			}
			setup = liveSetup(t)
		default:
			t.Fatalf("unknown -record=%s", *record)
		}
		rec := cassette.NewRecorder(*record)
		for name, p := range setup.Providers {
			setup.Providers[name] = rec.Wrap(p)
		}
		o, err := evals.RunReviewCase(context.Background(), c, setup)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		outcomes = append(outcomes, o)
		if err := rec.Cassette().Save(reviewCassettePath(c)); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("linguistic eval report (%s, recorded):\n%s", *record, evals.SummarizeReviews(outcomes))
}

func scriptedReviewAnswers(c evals.ReviewCase) (map[domain.Task][]string, error) {
	raw, err := os.ReadFile(filepath.Join(linguisticData, "scripted", c.Pair()+".json"))
	if err != nil {
		return nil, err
	}
	var all map[string]map[domain.Task][]string
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	answers, ok := all[c.ID]
	if !ok {
		return nil, fmt.Errorf("%s: no scripted answers", c.ID)
	}
	return answers, nil
}
