package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/prompts"
)

// The linguistic reviewer (RFC 0005 §3.8): one translation, one model
// call, a structurally constrained answer, and nothing on malformation.
//
// It is deliberately not an agent. The translation agent gathers,
// drafts, validates and repairs, because a draft that is structurally
// wrong can be told what is wrong and asked again, and the second
// answer is checkable against the same objective gate. A review has no
// such gate: if the model answers with prose instead of the structure,
// the only thing a second turn buys is an opinion invented to satisfy
// the format. So the answer is parsed once and refused once.
//
// Everything else is M2's, unchanged: the Router picks the provider and
// model by the routing policy, checks the per-tenant budget before the
// call and records the spend after it (RFC 0003 §3.1), and the
// sensitive-namespace and consent rules of §7 are checked here as they
// are in the agent's tools.

// ReviewCode is one code a review may report, with what it covers. The
// caller owns the list — the Quality context's linguistic layer is what
// a policy selects on — and this package only renders it into the
// prompt, constrains the schema to it and refuses anything outside it.
type ReviewCode struct {
	Code    string
	Meaning string
}

// MaxReviewNotes is the most notes one review may report. It is a
// prompt instruction and a hard limit: a model that answers with
// forty remarks about one user-interface string is not reviewing it,
// and an answer over the cap is refused rather than truncated, because
// silently keeping the first eight would keep an arbitrary eight.
const MaxReviewNotes = 8

// DefaultLinguisticPrompt is the linguistic prompt version in use.
const DefaultLinguisticPrompt = "v1"

// ReviewConfig configures a Reviewer.
type ReviewConfig struct {
	Router *Router
	// Codes is the vocabulary, which must not be empty.
	Codes []ReviewCode
	// Prompt is the linguistic prompt's version (default
	// DefaultLinguisticPrompt).
	Prompt string
	// MaxNotes caps one answer's notes (default MaxReviewNotes).
	MaxNotes int
}

// Reviewer runs the linguistic review for one translation at a time. It
// is safe for concurrent use.
type Reviewer struct {
	cfg   ReviewConfig
	tmpl  *prompts.Template
	codes []string
}

// NewReviewer validates cfg and loads the pinned prompt.
func NewReviewer(cfg ReviewConfig) (*Reviewer, error) {
	if cfg.Router == nil {
		return nil, errors.New("reviewer: a router is required")
	}
	if len(cfg.Codes) == 0 {
		return nil, errors.New("reviewer: the finding codes are required")
	}
	if cfg.Prompt == "" {
		cfg.Prompt = DefaultLinguisticPrompt
	}
	if cfg.MaxNotes <= 0 || cfg.MaxNotes > MaxReviewNotes {
		cfg.MaxNotes = MaxReviewNotes
	}
	tmpl, err := prompts.Load(prompts.Linguistic, cfg.Prompt)
	if err != nil {
		return nil, err
	}
	r := &Reviewer{cfg: cfg, tmpl: tmpl}
	for _, c := range cfg.Codes {
		r.codes = append(r.codes, c.Code)
	}
	return r, nil
}

// PromptVersion is "linguistic/<version>", as recorded on every finding
// the review produces.
func (r *Reviewer) PromptVersion() string { return r.tmpl.ID() }

// ReviewResult is one review's outcome.
type ReviewResult struct {
	// Notes are the accepted notes, in the order the model gave them.
	Notes []domain.ReviewNote `json:"notes"`
	// SourceText and TargetText are the texts the notes' spans are byte
	// offsets into. The caller stores nothing without them: a span means
	// nothing apart from the text it was measured in.
	SourceText string `json:"source_text"`
	TargetText string `json:"target_text"`

	PromptVersion string          `json:"prompt_version"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	Usage         domain.Usage    `json:"usage"`
	Cost          domain.MicroUSD `json:"cost"`
	Attempts      []Attempt       `json:"attempts,omitempty"`
	// Disclosures record which provider saw the text (RFC 0003 §7).
	// They are filled whether or not the answer was usable: a provider
	// that was tried saw the prompt.
	Disclosures []Disclosure `json:"disclosures,omitempty"`
}

// Review asks a model what is linguistically wrong with one
// translation. It returns ErrSensitive or ErrProviderConsent for the
// requests that never reach a provider, ErrMalformedReview (wrapping
// ErrInvalidOutput) for an answer that was not the constrained
// structure, and the provider or budget error that stopped the call.
//
// A successful review with no notes is a success: the model found
// nothing, which is the common case and the point.
func (r *Reviewer) Review(ctx context.Context, req domain.ReviewRequest) (ReviewResult, error) {
	if err := req.Validate(); err != nil {
		return ReviewResult{}, err
	}
	if req.Sensitive() {
		return ReviewResult{}, domain.ErrSensitive
	}
	if !req.ProviderConsent {
		return ReviewResult{}, domain.ErrProviderConsent
	}
	source, _, err := domain.ParseMessage(req.Source)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("reviewer: source message: %w", err)
	}
	translation, _, err := domain.ParseMessage(req.Translation)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("reviewer: translation: %w", err)
	}
	out := ReviewResult{
		SourceText:    domain.PlainText(source),
		TargetText:    domain.PlainText(translation),
		PromptVersion: r.PromptVersion(),
	}
	rendered, err := r.tmpl.Render(r.data(req, out.SourceText, out.TargetText, source))
	if err != nil {
		return out, err
	}
	messages := []domain.Message{{Role: domain.RoleUser, Text: rendered.User}}
	routed, callErr := r.cfg.Router.Complete(ctx, req.Scope, req.TargetLocale, domain.CompletionRequest{
		Task:     domain.TaskReview,
		System:   []domain.SystemBlock{{Text: rendered.System, Cacheable: true}},
		Messages: messages,
		Output:   &domain.OutputSchema{Name: "linguistic_review", Schema: prompts.LinguisticSchema(r.codes)},
	})
	systemSHA := sha(rendered.System)
	for _, a := range routed.Attempts {
		out.Disclosures = append(out.Disclosures, Disclosure{
			Task: domain.TaskReview, Provider: a.Provider, Model: a.Model,
			MessageID: req.MessageID, Sent: messages, SystemSHA256: systemSHA,
		})
	}
	out.Attempts = routed.Attempts
	if callErr != nil {
		return out, callErr
	}
	out.Provider, out.Model = routed.Route.Provider, routed.Route.Model
	out.Usage, out.Cost = routed.Usage, routed.Cost
	if routed.Stop == domain.StopMaxTokens {
		return out, fmt.Errorf("%w: the answer was cut off at max_tokens", domain.ErrMalformedReview)
	}
	notes, err := ParseReview(routed.Text, r.codes, r.cfg.MaxNotes, out.SourceText, out.TargetText)
	if err != nil {
		return out, err
	}
	out.Notes = notes
	return out, nil
}

// data fills the prompt from the request only, so it holds exactly what
// the caller passed and nothing the reviewer went and fetched.
func (r *Reviewer) data(req domain.ReviewRequest, sourceText, targetText string, source mf.Message) prompts.Data {
	d := prompts.Data{
		SourceLocale: req.SourceLocale, TargetLocale: req.TargetLocale,
		SourceText: sourceText, TranslationText: targetText,
		MaxFindings: r.cfg.MaxNotes,
		Context:     prompts.Context{Key: req.Key, Namespace: req.Namespace, Description: req.Description},
		Terms:       promptTerms(req.Terms),
	}
	for _, c := range r.cfg.Codes {
		d.Codes = append(d.Codes, prompts.Code{Code: c.Code, Meaning: c.Meaning})
	}
	for _, a := range mf.Arguments(source) {
		pa := prompts.Argument{Name: a.Name, Type: string(a.Type)}
		if a.Selector != nil {
			pa.Selector, pa.Keys = string(a.Selector.Kind), a.Selector.Keys
		}
		d.Arguments = append(d.Arguments, pa)
	}
	if req.Style != nil {
		d.Style = promptStyle(*req.Style)
	}
	return d
}

// ParseReview parses a model's review answer and refuses it whole on
// anything the contract does not allow.
//
// Refusing the whole answer rather than the offending note is the
// point. A model that invented one code invented the note it attached
// to it, and an answer that is half structure and half prose is not a
// review with a flaw in it — it is a model that did not do what it was
// asked. Keeping the parts that happen to parse would put an opinion
// nobody can trace in front of a translator.
//
// It is exported and pure so the refusal can be tested without a
// provider, a network or a cassette.
func ParseReview(answer string, codes []string, maxNotes int, sourceText, targetText string) ([]domain.ReviewNote, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil, fmt.Errorf("%w: the answer was empty", domain.ErrMalformedReview)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(answer)))
	dec.DisallowUnknownFields()
	var ans prompts.LinguisticAnswer
	if err := dec.Decode(&ans); err != nil {
		return nil, fmt.Errorf("%w: %w (answer began %q)", domain.ErrMalformedReview, err, head(answer))
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: the answer carried more than one JSON value", domain.ErrMalformedReview)
	}
	if len(ans.Findings) > maxNotes {
		return nil, fmt.Errorf("%w: %d findings, at most %d were asked for", domain.ErrMalformedReview, len(ans.Findings), maxNotes)
	}
	out := make([]domain.ReviewNote, 0, len(ans.Findings))
	for i, n := range ans.Findings {
		note, err := parseNote(n, codes, sourceText, targetText)
		if err != nil {
			return nil, fmt.Errorf("%w: finding %d: %w", domain.ErrMalformedReview, i+1, err)
		}
		out = append(out, note)
	}
	return out, nil
}

func parseNote(n prompts.ReviewNote, codes []string, sourceText, targetText string) (domain.ReviewNote, error) {
	if !contains(codes, n.Code) {
		return domain.ReviewNote{}, fmt.Errorf("code %q is not one of %s", n.Code, strings.Join(codes, ", "))
	}
	side := domain.ReviewSide(n.Side)
	if !side.Valid() {
		return domain.ReviewNote{}, fmt.Errorf("side %q is neither source nor target", n.Side)
	}
	if strings.TrimSpace(n.Explanation) == "" {
		return domain.ReviewNote{}, errors.New("the explanation is empty")
	}
	if n.Quote == "" {
		return domain.ReviewNote{}, errors.New("the quote is empty")
	}
	text := targetText
	if side == domain.ReviewSideSource {
		text = sourceText
	}
	// The quote is checked against the text it claims to be from. A
	// model that paraphrased, translated or invented it has pointed at
	// words that are not there, and an underline drawn from a guess is
	// worse than none.
	start := strings.Index(text, n.Quote)
	if start < 0 {
		return domain.ReviewNote{}, fmt.Errorf("the %s text does not contain the quote %q", side, head(n.Quote))
	}
	return domain.ReviewNote{
		Code: n.Code, Side: side, Quote: n.Quote,
		Start: start, End: start + len(n.Quote),
		Explanation: strings.TrimSpace(n.Explanation),
		Suggestion:  strings.TrimSpace(n.Suggestion),
	}, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// head is the first 80 characters of s, for an error that has to quote
// an answer without reprinting it.
func head(s string) string {
	r := []rune(s)
	if len(r) <= 80 {
		return s
	}
	return string(r[:80]) + "…"
}
