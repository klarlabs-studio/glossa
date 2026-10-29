package evals_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/evals"
)

var updateBaseline = flag.Bool("update-baseline", false, "write testdata/baseline.json from this run")

const testdata = "testdata"

func loadCases(t *testing.T) []evals.Case {
	t.Helper()
	cases, err := evals.LoadCases(testdata)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func cassettePath(c evals.Case) string {
	return filepath.Join(testdata, "cassettes", c.Pair(), c.ID+".json")
}

// The golden sets themselves must be right: every source and reference
// is valid MF2, and every reference is structurally compatible with its
// source for the target locale (no errors and no warnings).
func TestGoldenSetsAreValid(t *testing.T) {
	cases := loadCases(t)
	pairs := map[string]int{}
	for _, c := range cases {
		pairs[c.Pair()]++
		src, err := mf.ParseMF2(c.Source)
		if err != nil {
			t.Errorf("%s: source: %v", c.ID, err)
			continue
		}
		if len(c.References) == 0 {
			t.Errorf("%s: no references", c.ID)
		}
		for _, ref := range c.References {
			tr, err := mf.ParseMF2(ref)
			if err != nil {
				t.Errorf("%s: reference %q: %v", c.ID, ref, err)
				continue
			}
			if f := mf.CheckCompat(src, tr, c.TargetLocale); len(f) != 0 {
				t.Errorf("%s: reference %q: %+v", c.ID, ref, f)
			}
		}
	}
	for _, want := range []string{"de-en", "en-de", "de-fr", "de-es", "en-ja", "en-pl"} {
		if pairs[want] < 6 {
			t.Errorf("pair %s has %d cases, want at least 6", want, pairs[want])
		}
	}
}

// TestEvalsOnCassettes is the CI eval: the agent runs every golden case
// against recorded provider answers and must not regress the baseline.
func TestEvalsOnCassettes(t *testing.T) {
	ctx := context.Background()
	var outcomes []evals.Outcome
	for _, c := range loadCases(t) {
		cas, err := cassette.Load(cassettePath(c))
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		o, err := evals.RunCase(ctx, c, evals.Setup{
			Providers: map[string]domain.Provider{"anthropic": cas.Provider("anthropic")},
			Routing:   app.DefaultRouting(),
			Prices:    app.DefaultPrices(),
		})
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		if o.Structural && !o.ActionOK {
			t.Errorf("%s: routed to %s, want %s", c.ID, o.Action, c.Expect.Action)
		}
		if unused := cas.Unused(); len(unused) != 0 {
			t.Errorf("%s: %d recorded interactions were not replayed; re-record the cassette", c.ID, len(unused))
		}
		outcomes = append(outcomes, o)
	}
	report := evals.Summarize(outcomes)
	t.Logf("eval report (cassettes):\n%s", report)
	for _, o := range report.Outcomes {
		if !o.Structural || !o.OriginOK || (o.Terminology != nil && !*o.Terminology) || (o.Formality != nil && !*o.Formality) {
			t.Logf("  %-44s structural=%v origin=%v terms=%s formality=%s action=%s %s", o.Case, o.Structural, o.OriginOK, tri(o.Terminology), tri(o.Formality), o.Action, o.Error)
		}
	}

	path := filepath.Join(testdata, "baseline.json")
	if *updateBaseline {
		raw, _ := json.MarshalIndent(evals.BaselineOf(report), "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("baseline: %v (create it with -update-baseline)", err)
	}
	var base evals.Baseline
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	if regressions := report.Regressions(base); len(regressions) > 0 {
		t.Errorf("eval regressions against %s:\n  %s", path, strings.Join(regressions, "\n  "))
	}
}

// A cassette that no longer matches the prompts fails loudly.
func TestCassetteMismatchFailsLoudly(t *testing.T) {
	cases := loadCases(t)
	var c evals.Case
	for _, x := range cases {
		if x.ID == "en-de-greeting" {
			c = x
		}
	}
	cas, err := cassette.Load(cassettePath(c))
	if err != nil {
		t.Fatal(err)
	}
	c.Context.Description = "A different description changes the prompt"
	_, err = evals.RunCase(context.Background(), c, evals.Setup{
		Providers: map[string]domain.Provider{"anthropic": cas.Provider("anthropic")},
		Routing:   app.DefaultRouting(), Prices: app.DefaultPrices(),
	})
	if err == nil || !strings.Contains(err.Error(), "description: A different description") || !strings.Contains(err.Error(), "TestRecord.*Cassettes") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditRatioAndSummaries(t *testing.T) {
	yes, no := true, false
	r := evals.Summarize([]evals.Outcome{
		{Case: "a", Pair: "en-de", Structural: true, OriginOK: true, Terminology: &yes, Formality: &no, EditRatio: 0, Exact: true, Confidence: 0.8, Action: "approve_recommended", Calls: 2, CostMicros: 100},
		{Case: "b", Pair: "en-de", Error: "invalid_output", EditRatio: 1, Calls: 3, CostMicros: 50},
	})
	p := r.Pairs[0]
	if p.StructuralPassRate != 0.5 || p.TerminologyCompliance != 1 || p.FormalityCompliance != 0 || p.MeanEditRatio != 0.5 ||
		p.ExactRate != 0.5 || p.MeanConfidence != 0.8 || p.Actions["failed"] != 1 || p.CostMicroUSD != 150 {
		t.Errorf("pair report = %+v", p)
	}
	base := evals.BaselineOf(r)
	if len(r.Regressions(base)) != 0 {
		t.Error("a report does not regress against itself")
	}
	worse := base["en-de"]
	worse.StructuralPassRate = 1
	base["en-de"] = worse
	if len(r.Regressions(base)) != 1 {
		t.Error("a lower pass rate is a regression")
	}
	if !strings.Contains(r.String(), "en-de") {
		t.Error("table lacks the pair")
	}
}

func tri(b *bool) string {
	if b == nil {
		return "-"
	}
	if *b {
		return "ok"
	}
	return "FAIL"
}
