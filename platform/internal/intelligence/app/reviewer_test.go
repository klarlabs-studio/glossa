package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/memory"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// The linguistic reviewer (RFC 0005 §3.8). Every test here answers from
// a stub in this file: the reviewer is only ever handed a provider map
// the test built, so no test can reach a network even by accident.

var reviewCodes = []app.ReviewCode{
	{Code: "meaning-divergence", Meaning: "the translation states something the source does not"},
	{Code: "tone-mismatch", Meaning: "the register contradicts the style guide"},
	{Code: "grammar-suspected", Meaning: "the target text is ungrammatical"},
	{Code: "inconsistent-phrasing", Meaning: "the wording is not this product's"},
}

const sourceText, targetText = "You have new files.", "Du hast neue Dateien."

// answering is a provider that says exactly what the test wants said.
type answering struct {
	text  string
	stop  domain.StopReason
	err   error
	got   []domain.CompletionRequest
	usage domain.Usage
}

func (a *answering) Name() string { return "anthropic" }

func (a *answering) Complete(_ context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	a.got = append(a.got, req)
	stop := a.stop
	if stop == "" {
		stop = domain.StopEnd
	}
	return domain.Completion{Provider: "anthropic", Model: req.Model, Text: a.text, Stop: stop, Usage: a.usage}, a.err
}

func reviewerFixture(t *testing.T, p domain.Provider) (*app.Reviewer, *memory.Budget) {
	t.Helper()
	budget := memory.NewBudget(map[string]domain.MicroUSD{"t1": 1_000_000})
	router := app.NewRouter(map[string]domain.Provider{"anthropic": p}, app.DefaultRouting(), app.DefaultPrices(), budget)
	r, err := app.NewReviewer(app.ReviewConfig{Router: router, Codes: reviewCodes})
	if err != nil {
		t.Fatal(err)
	}
	return r, budget
}

func reviewReq() domain.ReviewRequest {
	return domain.ReviewRequest{
		Scope: domain.Scope{TenantID: "t1", ProjectID: "p1"}, MessageID: "m_1", Key: "files.count",
		SourceLocale: "en", TargetLocale: "de",
		Source: "You have new files.", Translation: "Du hast neue Dateien.",
		ProviderConsent: true,
	}
}

func TestReviewParsesTheConstrainedAnswer(t *testing.T) {
	p := &answering{text: `{"findings":[{"code":"tone-mismatch","side":"target","quote":"Du hast","explanation":"The guide asks for Sie.","suggestion":"Sie haben"}]}`,
		usage: domain.Usage{InputTokens: 500, OutputTokens: 60}}
	r, budget := reviewerFixture(t, p)
	out, err := r.Review(context.Background(), reviewReq())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Notes) != 1 {
		t.Fatalf("notes = %+v", out.Notes)
	}
	n := out.Notes[0]
	switch {
	case n.Code != "tone-mismatch" || n.Side != domain.ReviewSideTarget:
		t.Errorf("note = %+v", n)
	case n.Start != 0 || n.End != len("Du hast"):
		t.Errorf("span = %d-%d, want the quote resolved in the target text", n.Start, n.End)
	case out.TargetText[n.Start:n.End] != n.Quote:
		t.Errorf("the span does not cover the quote: %q", out.TargetText[n.Start:n.End])
	case n.Suggestion != "Sie haben":
		t.Errorf("suggestion = %q", n.Suggestion)
	case out.PromptVersion != "linguistic/v1":
		t.Errorf("prompt version = %q", out.PromptVersion)
	case out.SourceText != sourceText || out.TargetText != targetText:
		t.Errorf("texts = %q / %q, want the literal text the spans are measured in", out.SourceText, out.TargetText)
	}
	// The call is the tenant's, on the tenant's budget (RFC 0005 §10).
	if budget.Spent("t1") == 0 {
		t.Error("the review spent nothing against the tenant's AI budget")
	}
	if len(out.Disclosures) != 1 || out.Disclosures[0].Task != domain.TaskReview || out.Disclosures[0].MessageID != "m_1" {
		t.Errorf("disclosures = %+v, want the provider that saw the text recorded", out.Disclosures)
	}
	// The answer is constrained at the provider too: the schema carries
	// the vocabulary, so a code outside it is refused before it is sent.
	sent := p.got[0]
	if sent.Output == nil || sent.Output.Name != "linguistic_review" {
		t.Fatalf("output schema = %+v", sent.Output)
	}
	if sent.Task != domain.TaskReview {
		t.Errorf("task = %q, want the routing policy's review task", sent.Task)
	}
	if !strings.Contains(sent.System[0].Text, "`tone-mismatch`") {
		t.Error("the system prompt does not list the vocabulary")
	}
}

// A sound translation is a successful review with nothing to say.
func TestReviewAcceptsAnEmptyAnswer(t *testing.T) {
	r, _ := reviewerFixture(t, &answering{text: `{"findings":[]}`})
	out, err := r.Review(context.Background(), reviewReq())
	if err != nil || len(out.Notes) != 0 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

// RFC 0005 §3.8: the answer is parsed and rejected on malformation,
// exactly as the translation agent's drafts are. A model that returns
// prose instead of the structure fails loudly; it is not coaxed, and no
// part of a bad answer survives.
func TestAMalformedAnswerIsRejectedWhole(t *testing.T) {
	good := `{"code":"tone-mismatch","side":"target","quote":"Du hast","explanation":"x","suggestion":""}`
	for name, answer := range map[string]string{
		"prose":                   "The translation looks fine to me, though the tone is a little casual.",
		"empty":                   "   ",
		"markdown-fenced":         "```json\n{\"findings\":[]}\n```",
		"not an object":           `[{"code":"tone-mismatch"}]`,
		"unknown field":           `{"findings":[],"confidence":0.8}`,
		"invented code":           `{"findings":[{"code":"semantic-divergence","side":"target","quote":"Du hast","explanation":"x","suggestion":""}]}`,
		"invented side":           `{"findings":[{"code":"tone-mismatch","side":"both","quote":"Du hast","explanation":"x","suggestion":""}]}`,
		"empty explanation":       `{"findings":[{"code":"tone-mismatch","side":"target","quote":"Du hast","explanation":" ","suggestion":""}]}`,
		"empty quote":             `{"findings":[{"code":"tone-mismatch","side":"target","quote":"","explanation":"x","suggestion":""}]}`,
		"quote not in text":       `{"findings":[{"code":"tone-mismatch","side":"target","quote":"Sie haben","explanation":"x","suggestion":""}]}`,
		"quote on the wrong side": `{"findings":[{"code":"tone-mismatch","side":"source","quote":"Du hast","explanation":"x","suggestion":""}]}`,
		"trailing value":          `{"findings":[]} {"findings":[]}`,
		"over the cap":            `{"findings":[` + strings.TrimSuffix(strings.Repeat(good+",", 9), ",") + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := reviewerFixture(t, &answering{text: answer})
			out, err := r.Review(context.Background(), reviewReq())
			if !errors.Is(err, domain.ErrMalformedReview) {
				t.Fatalf("err = %v, want ErrMalformedReview", err)
			}
			if !errors.Is(err, domain.ErrInvalidOutput) {
				t.Error("a malformed review is not an invalid_output failure")
			}
			if len(out.Notes) != 0 {
				t.Errorf("notes = %+v, want nothing from a refused answer", out.Notes)
			}
		})
	}
}

// An answer cut off at max_tokens is half an opinion, and half an
// opinion is refused like any other malformation.
func TestATruncatedAnswerIsRejected(t *testing.T) {
	r, _ := reviewerFixture(t, &answering{text: `{"findings":[]}`, stop: domain.StopMaxTokens})
	if _, err := r.Review(context.Background(), reviewReq()); !errors.Is(err, domain.ErrMalformedReview) {
		t.Fatalf("err = %v, want ErrMalformedReview", err)
	}
}

// RFC 0003 §7 holds here exactly as it does for translation: a
// `sensitive` namespace never reaches a provider, and neither does a
// tenant that has not consented to sending text.
func TestSensitiveAndUnconsentedRequestsNeverReachAProvider(t *testing.T) {
	sensitive := reviewReq()
	sensitive.Tags = []string{domain.TagSensitive}
	p := &answering{text: `{"findings":[]}`}
	r, _ := reviewerFixture(t, p)
	if _, err := r.Review(context.Background(), sensitive); !errors.Is(err, domain.ErrSensitive) {
		t.Fatalf("err = %v, want ErrSensitive", err)
	}
	unconsented := reviewReq()
	unconsented.ProviderConsent = false
	if _, err := r.Review(context.Background(), unconsented); !errors.Is(err, domain.ErrProviderConsent) {
		t.Fatalf("err = %v, want ErrProviderConsent", err)
	}
	if len(p.got) != 0 {
		t.Errorf("the provider was called %d times", len(p.got))
	}
}

// RFC 0005 §10: the layer is bounded by the existing per-tenant AI
// budget, and there is no second one. A tenant over its cap is refused
// before the call, not after it.
func TestTheReviewIsBoundedByTheTenantBudget(t *testing.T) {
	p := &answering{text: `{"findings":[]}`}
	budget := memory.NewBudget(map[string]domain.MicroUSD{"t1": 1})
	router := app.NewRouter(map[string]domain.Provider{"anthropic": p}, app.DefaultRouting(), app.DefaultPrices(), budget)
	r, err := app.NewReviewer(app.ReviewConfig{Router: router, Codes: reviewCodes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Review(context.Background(), reviewReq()); !errors.Is(err, domain.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if len(p.got) != 0 {
		t.Error("the provider was called although the tenant is over its budget")
	}
}

// ParseReview is the refusal on its own, without a provider at all.
func TestParseReviewResolvesSpansInTheRightText(t *testing.T) {
	codes := []string{"meaning-divergence", "tone-mismatch"}
	notes, err := app.ParseReview(
		`{"findings":[{"code":"meaning-divergence","side":"source","quote":"new files","explanation":"The target says old.","suggestion":""}]}`,
		codes, app.MaxReviewNotes, sourceText, targetText)
	if err != nil {
		t.Fatal(err)
	}
	n := notes[0]
	if n.Side != domain.ReviewSideSource || sourceText[n.Start:n.End] != "new files" {
		t.Errorf("note = %+v", n)
	}
	if n.Suggestion != "" {
		t.Errorf("suggestion = %q, want the optional field empty", n.Suggestion)
	}
}

func TestNewReviewerNeedsARouterAndAVocabulary(t *testing.T) {
	if _, err := app.NewReviewer(app.ReviewConfig{Codes: reviewCodes}); err == nil {
		t.Error("want an error without a router")
	}
	router := app.NewRouter(nil, app.DefaultRouting(), app.DefaultPrices(), domain.NoBudget{})
	if _, err := app.NewReviewer(app.ReviewConfig{Router: router}); err == nil {
		t.Error("want an error without codes")
	}
	if _, err := app.NewReviewer(app.ReviewConfig{Router: router, Codes: reviewCodes, Prompt: "v0"}); err == nil {
		t.Error("want an error for an unknown prompt version")
	}
}
