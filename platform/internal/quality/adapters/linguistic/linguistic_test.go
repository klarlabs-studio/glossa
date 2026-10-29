package linguistic_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/memory"
	intel "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/linguistic"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The seam between the two contexts: Quality names the vocabulary,
// Intelligence spends the money, and what comes back is a finding like
// every other layer's.
//
// Every provider in this file is a struct two lines above the test.
// Nothing here can reach a network: the Router is handed a map the test
// built, and a route naming a provider that map does not hold fails
// with "unconfigured provider" rather than dialling anything.

type answering struct {
	text string
	got  []inteldomain.CompletionRequest
}

func (a *answering) Name() string { return "anthropic" }

func (a *answering) Complete(_ context.Context, req inteldomain.CompletionRequest) (inteldomain.Completion, error) {
	a.got = append(a.got, req)
	return inteldomain.Completion{
		Provider: "anthropic", Model: req.Model, Text: a.text, Stop: inteldomain.StopEnd,
		Usage: inteldomain.Usage{InputTokens: 400, OutputTokens: 40},
	}, nil
}

func reviewer(t *testing.T, answer string) (*linguistic.Reviewer, *memory.Budget) {
	t.Helper()
	budget := memory.NewBudget(map[string]inteldomain.MicroUSD{"t1": 1_000_000})
	router := intel.NewRouter(
		map[string]inteldomain.Provider{"anthropic": &answering{text: answer}},
		intel.DefaultRouting(), intel.DefaultPrices(), budget)
	r, err := linguistic.New(router)
	if err != nil {
		t.Fatal(err)
	}
	return r, budget
}

func request() linguistic.Request {
	return linguistic.Request{
		Tenant: "t1", Project: "p1", Message: "m_1", Key: "files.count", Namespace: "files",
		Revision: "r_7", SourceLocale: "en", TargetLocale: "de",
		Source: "You have new files.", Translation: "Du hast neue Dateien.",
		Style:           &inteldomain.StyleGuide{Version: "v3", Formality: "formal", Pronoun: "Sie"},
		ProviderConsent: true,
	}
}

func TestAReviewBecomesTheLayer(t *testing.T) {
	r, budget := reviewer(t, `{"findings":[{"code":"tone-mismatch","side":"target","quote":"Du hast","explanation":"The guide asks for Sie.","suggestion":"Sie haben"}]}`)
	out, err := r.Review(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	fs := out.Findings()
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
	f := fs[0]
	switch {
	case f.Layer != domain.LayerLinguistic:
		t.Errorf("layer = %q", f.Layer)
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning; a build never fails on an opinion", f.Severity)
	case f.Code != layers.CodeToneMismatch:
		t.Errorf("code = %q", f.Code)
	case f.Locus.Message != "m_1" || f.Locus.Locale != "de" || f.Locus.Revision != "r_7" || f.Locus.Namespace != "files":
		t.Errorf("locus = %+v", f.Locus)
	case f.Locus.Span == nil || f.Locus.Span.Side != domain.SideTarget || f.Locus.Span.End != len("Du hast"):
		t.Errorf("span = %+v", f.Locus.Span)
	case f.Fix == nil || f.Fix.Hint != "Sie haben":
		t.Errorf("fix = %+v", f.Fix)
	case f.Evidence[layers.EvidencePromptVersion] != "linguistic/v1":
		t.Errorf("evidence = %+v", f.Evidence)
	case f.Evidence[layers.EvidenceProvider] != "anthropic":
		t.Errorf("evidence = %+v", f.Evidence)
	}
	if out.PromptVersion != r.PromptVersion() || out.PromptVersion != "linguistic/v1" {
		t.Errorf("prompt version = %q", out.PromptVersion)
	}
	if budget.Spent("t1") == 0 {
		t.Error("the review spent nothing against the tenant's AI budget")
	}
	// The layer reports what the job computed, and computes nothing.
	if got := out.Layer.Check(nil, checkpolicy.Policy{}); len(got) != 1 {
		t.Errorf("Check() = %+v", got)
	}
}

// A refused answer yields no layer at all: half a review is not
// evidence, so nothing is stored and the job fails.
func TestAMalformedAnswerYieldsNoFindings(t *testing.T) {
	r, _ := reviewer(t, "I think the translation is fine, but the tone is casual.")
	out, err := r.Review(context.Background(), request())
	if !errors.Is(err, inteldomain.ErrMalformedReview) {
		t.Fatalf("err = %v, want ErrMalformedReview", err)
	}
	if len(out.Findings()) != 0 || len(out.Reviewed) != 0 {
		t.Errorf("out = %+v, want nothing from a refused answer", out)
	}
}

// The fingerprint is hashed over the message ID (RFC 0005 §2.1), so a
// caller that has not resolved one is refused rather than allowed to
// mint an identity no other surface would compute.
func TestAReviewNeedsTheMessageID(t *testing.T) {
	r, _ := reviewer(t, `{"findings":[]}`)
	req := request()
	req.Message = ""
	if _, err := r.Review(context.Background(), req); err == nil {
		t.Fatal("want an error without a message ID")
	}
}

// One vocabulary: what the layer grades is what the reviewer asks for.
func TestTheVocabularyIsTheLayers(t *testing.T) {
	codes := linguistic.Codes()
	if len(codes) != len(layers.LinguisticCodes) {
		t.Fatalf("codes = %+v", codes)
	}
	for i, c := range codes {
		if c.Code != layers.LinguisticCodes[i].Code || c.Meaning != layers.LinguisticCodes[i].Meaning {
			t.Errorf("code %d = %+v, want the layer's %+v", i, c, layers.LinguisticCodes[i])
		}
	}
}
