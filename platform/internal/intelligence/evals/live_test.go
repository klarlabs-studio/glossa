//go:build live

package evals_test

import (
	"context"
	"flag"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/anthropic"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/gemini"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/openaicompat"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/resilient"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/evals"
)

// Live mode runs the agent against real providers. It never runs in CI:
// it needs the build tag and an API key, and it spends money.
//
//	go test -tags=live ./internal/intelligence/evals -run TestLiveEval -v            # report only
//	go test -tags=live ./internal/intelligence/evals -run 'TestRecord.*Cassettes' -record=live
//
// -provider picks anthropic (ANTHROPIC_API_KEY), openai (OPENAI_API_KEY,
// OPENAI_BASE_URL optional) or gemini (GEMINI_API_KEY); -model and
// -assess-model override the routed models. Recorded cassettes keep the
// provider name "anthropic" only for anthropic; others record under their
// own name, so replaying them needs the same -provider.
var (
	liveProvider    = flag.String("provider", "anthropic", "live provider: anthropic, openai or gemini")
	liveModel       = flag.String("model", "", "translate model (default: the provider's default)")
	liveAssessModel = flag.String("assess-model", "", "assess model (default: the provider's default)")
)

func init() { liveSetup = newLiveSetup }

func newLiveSetup(t *testing.T) evals.Setup {
	t.Helper()
	var (
		p           domain.Provider
		err         error
		model       string
		assessModel string
	)
	switch *liveProvider {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			t.Skip("ANTHROPIC_API_KEY is not set")
		}
		p, err = anthropic.New(anthropic.Config{APIKey: key})
		model, assessModel = app.DefaultTranslateModel, app.DefaultAssessModel
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			t.Skip("OPENAI_API_KEY is not set")
		}
		base := os.Getenv("OPENAI_BASE_URL")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		p, err = openaicompat.New(openaicompat.Config{Name: "openai", BaseURL: base, APIKey: key, MaxCompletionTokens: true, StrictSchema: true})
		model, assessModel = "gpt-5", "gpt-5-mini"
	case "gemini":
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			t.Skip("GEMINI_API_KEY is not set")
		}
		p, err = gemini.New(gemini.Config{APIKey: key})
		model, assessModel = "gemini-2.5-pro", "gemini-2.5-flash"
	default:
		t.Fatalf("unknown -provider=%s", *liveProvider)
	}
	if err != nil {
		t.Fatal(err)
	}
	if *liveModel != "" {
		model = *liveModel
	}
	if *liveAssessModel != "" {
		assessModel = *liveAssessModel
	}
	name := p.Name()
	translate := domain.Route{Provider: name, Model: model, MaxTokens: 8192}
	if name == "anthropic" {
		translate.Effort = "medium"
	}
	return evals.Setup{
		Providers: map[string]domain.Provider{name: resilient.Wrap(p, resilient.Config{})},
		Routing: domain.RoutingPolicy{Rules: []domain.RoutingRule{
			{Task: domain.TaskTranslate, Routes: []domain.Route{translate}},
			// The linguistic layer's reviews route like a translation:
			// judging a translation is not the cheap self-assessment.
			{Task: domain.TaskReview, Routes: []domain.Route{translate}},
			{Task: domain.TaskAssess, Routes: []domain.Route{{Provider: name, Model: assessModel, MaxTokens: 1024}}},
		}},
		Prices: app.DefaultPrices(),
	}
}

// TestLiveEval reports the metrics against the live provider without
// recording anything.
func TestLiveEval(t *testing.T) {
	setup := newLiveSetup(t)
	var outcomes []evals.Outcome
	for _, c := range loadCases(t) {
		o, err := evals.RunCase(context.Background(), c, setup)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		outcomes = append(outcomes, o)
	}
	t.Logf("eval report (live %s):\n%s", *liveProvider, evals.Summarize(outcomes))
}

// TestLiveLinguisticEval reports the linguistic layer's numbers against
// the live provider without recording anything. It is the only way to
// learn what the model actually notices: the cassettes measure the
// prompt, the schema and the parser, and a hand-written answer cannot
// tell you whether a real reviewer would have seen the problem.
func TestLiveLinguisticEval(t *testing.T) {
	setup := newLiveSetup(t)
	var outcomes []evals.ReviewOutcome
	for _, c := range loadReviewCases(t) {
		o, err := evals.RunReviewCase(context.Background(), c, setup)
		if err != nil {
			t.Fatalf("%s: %v", c.ID, err)
		}
		outcomes = append(outcomes, o)
	}
	t.Logf("linguistic eval report (live %s):\n%s", *liveProvider, evals.SummarizeReviews(outcomes))
}
