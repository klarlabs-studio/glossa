package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/prompts"
)

// The translation agent's tools (RFC 0003 §3.2). Each is bound to one job
// — its tenant, project, message and locales — so the planner can never
// widen its scope; the inputs only carry what varies within the job.
const (
	ToolTMLookup       = "tm_lookup"
	ToolTermLookup     = "term_lookup"
	ToolStyleRules     = "style_rules"
	ToolMessageContext = "message_context"
	ToolValidate       = "validate"
	ToolDraft          = "draft"
	ToolAssess         = "assess"
)

// Tool outputs. They are the agent's evidence: the audit ledger records
// them, and the planner and the Translator read them back.
type (
	tmOutput struct {
		Matches []domain.TMMatch `json:"matches"`
	}
	termOutput struct {
		Hits []domain.TermHit `json:"hits"`
	}
	styleOutput struct {
		Style domain.StyleGuide `json:"style"`
	}
	contextOutput struct {
		Context           domain.MessageContext `json:"context"`
		Arguments         []mf.Argument         `json:"arguments"`
		Markup            []string              `json:"markup"`
		PluralCategories  []string              `json:"plural_categories,omitempty"`
		OrdinalCategories []string              `json:"ordinal_categories,omitempty"`
	}

	// knowledge is what the gather tools returned, passed on verbatim to
	// the model-calling tools so a prompt holds exactly the tools' data.
	knowledge struct {
		TM      tmOutput      `json:"tm"`
		Terms   termOutput    `json:"terms"`
		Style   styleOutput   `json:"style"`
		Context contextOutput `json:"context"`
	}

	validateInput struct {
		Translation string `json:"translation"`
		// Origin is "translation_memory" (a TM candidate) or "draft".
		Origin    string `json:"origin"`
		UnitID    string `json:"unit_id,omitempty"`
		Attempt   int    `json:"attempt,omitempty"`
		MaxLength *int   `json:"max_length,omitempty"`
		// AnswerError reports a draft answer that was not the requested
		// JSON; it fails validation so the model repairs it.
		AnswerError string `json:"answer_error,omitempty"`
	}
	validateOutput struct {
		Origin       string               `json:"origin"`
		UnitID       string               `json:"unit_id,omitempty"`
		Attempt      int                  `json:"attempt,omitempty"`
		Valid        bool                 `json:"valid"`
		Structure    Structure            `json:"structure"`
		TermFindings []domain.TermFinding `json:"term_findings,omitempty"`
	}

	// attempt is one earlier draft and what validation found in it.
	attempt struct {
		Answer   string       `json:"answer"`
		Findings []mf.Finding `json:"findings"`
	}
	draftInput struct {
		Knowledge knowledge `json:"knowledge"`
		// Attempts are the earlier drafts; non-empty means repair.
		Attempts []attempt `json:"attempts,omitempty"`
	}
	// sent is exactly what a provider received, for privacy accounting.
	sent struct {
		SystemSHA256 string           `json:"system_sha256"`
		Messages     []domain.Message `json:"messages"`
	}
	callOutput struct {
		Provider      string          `json:"provider"`
		Model         string          `json:"model"`
		PromptVersion string          `json:"prompt_version"`
		Usage         domain.Usage    `json:"usage"`
		Cost          domain.MicroUSD `json:"cost"`
		Attempts      []Attempt       `json:"attempts"`
		Sent          sent            `json:"sent"`
	}
	draftOutput struct {
		Attempt int    `json:"attempt"`
		Message string `json:"message"`
		Notes   string `json:"notes,omitempty"`
		// AnswerError is set when the answer was not the requested JSON.
		AnswerError string `json:"answer_error,omitempty"`
		Answer      string `json:"answer"`
		callOutput
	}

	assessInput struct {
		Translation string    `json:"translation"`
		Knowledge   knowledge `json:"knowledge"`
	}
	assessOutput struct {
		// Skipped means no assessment (no route, provider or budget
		// failure); the score then goes without it.
		Skipped     bool     `json:"skipped,omitempty"`
		Reason      string   `json:"reason,omitempty"`
		Score       float64  `json:"score"`
		FormalityOK bool     `json:"formality_ok"`
		Issues      []string `json:"issues,omitempty"`
		callOutput
	}
)

// Disclosure records that a provider received a message's text (RFC 0003
// §7): which provider and model saw which message, and exactly what they
// were sent.
type Disclosure struct {
	Task      domain.Task      `json:"task"`
	Provider  string           `json:"provider"`
	Model     string           `json:"model"`
	MessageID string           `json:"message_id"`
	Sent      []domain.Message `json:"sent"`
	// SystemSHA256 identifies the system prompt (versioned, no data).
	SystemSHA256 string `json:"system_sha256"`
}

// job binds the tools to one translation request.
type job struct {
	req     domain.TranslationRequest
	source  mf.Message
	t       *Translator
	mu      sync.Mutex
	disc    []Disclosure
	toolErr error
}

func (j *job) scope() domain.Scope { return j.req.Scope }
func (j *job) pair() domain.LocalePair {
	return domain.LocalePair{Source: j.req.SourceLocale, Target: j.req.TargetLocale}
}

func (j *job) fail(err error) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.toolErr == nil {
		j.toolErr = err
	}
	return err
}

func (j *job) disclose(task domain.Task, attempts []Attempt, s sent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, a := range attempts {
		j.disc = append(j.disc, Disclosure{
			Task: task, Provider: a.Provider, Model: a.Model, MessageID: j.req.MessageID,
			Sent: s.Messages, SystemSHA256: s.SystemSHA256,
		})
	}
}

func (j *job) tmLookup(ctx context.Context) (tmOutput, error) {
	m, err := j.t.cfg.Knowledge.LookupTM(ctx, j.scope(), domain.TMQuery{Pair: j.pair(), Source: j.req.Source, Key: j.req.Key, Namespace: j.req.Namespace, Limit: j.t.cfg.TMLimit})
	return tmOutput{Matches: m}, err
}

func (j *job) termLookup(ctx context.Context) (termOutput, error) {
	h, err := j.t.cfg.Knowledge.RecognizeTerms(ctx, j.scope(), j.pair(), domain.PlainText(j.source))
	return termOutput{Hits: h}, err
}

func (j *job) styleRules(ctx context.Context) (styleOutput, error) {
	g, err := j.t.cfg.Knowledge.EffectiveStyle(ctx, j.scope(), j.req.TargetLocale, j.req.Namespace)
	return styleOutput{Style: g}, err
}

func (j *job) messageContext(ctx context.Context) (contextOutput, error) {
	c, err := j.t.cfg.Knowledge.MessageContext(ctx, j.scope(), j.req.MessageID, j.req.TargetLocale)
	if err != nil {
		return contextOutput{}, err
	}
	out := contextOutput{Context: c, Arguments: mf.Arguments(j.source)}
	for _, m := range mf.MarkupElements(j.source) {
		if !slices.Contains(out.Markup, m.Name) {
			out.Markup = append(out.Markup, m.Name)
		}
	}
	for _, a := range out.Arguments {
		if a.Selector == nil {
			continue
		}
		switch a.Selector.Kind {
		case mf.SelectPlural:
			if out.PluralCategories, err = domain.PluralCategories(j.req.TargetLocale, false); err != nil {
				return contextOutput{}, err
			}
		case mf.SelectOrdinal:
			if out.OrdinalCategories, err = domain.PluralCategories(j.req.TargetLocale, true); err != nil {
				return contextOutput{}, err
			}
		}
	}
	return out, nil
}

func (j *job) validate(ctx context.Context, in validateInput) (validateOutput, error) {
	if in.AnswerError != "" {
		return validateOutput{Origin: in.Origin, Attempt: in.Attempt, Structure: Structure{Errors: []mf.Finding{{
			Code: FindingAnswerFormat, Severity: mf.SeverityError,
			Message: in.AnswerError + `; answer with {"message": "<MF2>", "notes": "…"}`,
		}}}}, nil
	}
	s, msg := CheckStructure(j.source, in.Translation, j.req.TargetLocale, in.MaxLength)
	out := validateOutput{Origin: in.Origin, UnitID: in.UnitID, Attempt: in.Attempt, Valid: s.Valid(), Structure: s}
	if s.Valid() {
		f, err := j.t.cfg.Knowledge.CheckTerminology(ctx, j.scope(), j.pair(), domain.PlainText(j.source), domain.PlainText(msg))
		if err != nil {
			return validateOutput{}, err
		}
		out.TermFindings = f
	}
	return out, nil
}

// errSensitiveTool is the tools' own guard: even if a planner asked, a
// sensitive message never reaches a provider.
var errSensitiveTool = fmt.Errorf("%w (tool guard)", domain.ErrSensitive)

func (j *job) draft(ctx context.Context, in draftInput) (draftOutput, error) {
	if j.req.Sensitive() {
		return draftOutput{}, j.fail(errSensitiveTool)
	}
	if !j.req.ProviderConsent {
		return draftOutput{}, j.fail(domain.ErrProviderConsent)
	}
	data := j.promptData(in.Knowledge)
	system, err := j.t.translate.Render(data)
	if err != nil {
		return draftOutput{}, j.fail(err)
	}
	messages := []domain.Message{{Role: domain.RoleUser, Text: system.User}}
	version := j.t.translate.ID()
	for i, a := range in.Attempts {
		d := data
		d.Attempt, d.MaxAttempts = i+1, j.t.cfg.MaxRepairs
		for _, f := range a.Findings {
			d.Findings = append(d.Findings, prompts.Finding{Code: string(f.Code), Subject: f.Subject, Message: f.Message})
		}
		repair, err := j.t.repair.Render(d)
		if err != nil {
			return draftOutput{}, j.fail(err)
		}
		messages = append(messages,
			domain.Message{Role: domain.RoleAssistant, Text: a.Answer},
			domain.Message{Role: domain.RoleUser, Text: repair.User})
		version = j.t.translate.ID() + "+" + j.t.repair.ID()
	}
	req := domain.CompletionRequest{
		Task:     domain.TaskTranslate,
		System:   []domain.SystemBlock{{Text: system.System, Cacheable: true}},
		Messages: messages,
		Output:   &domain.OutputSchema{Name: "translation", Schema: prompts.DraftSchema()},
	}
	routed, err := j.t.router.Complete(ctx, j.scope(), j.req.TargetLocale, req)
	s := sent{SystemSHA256: sha(system.System), Messages: messages}
	j.disclose(domain.TaskTranslate, routed.Attempts, s)
	if err != nil {
		return draftOutput{}, j.fail(err)
	}
	out := draftOutput{
		Attempt: len(in.Attempts) + 1,
		Answer:  routed.Text,
		callOutput: callOutput{
			Provider: routed.Route.Provider, Model: routed.Route.Model, PromptVersion: version,
			Usage: routed.Usage, Cost: routed.Cost, Attempts: routed.Attempts, Sent: s,
		},
	}
	var ans prompts.DraftAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(routed.Text)), &ans); err != nil || ans.Message == "" {
		out.AnswerError = "the answer is not the requested JSON object with a message"
	} else {
		out.Message, out.Notes = ans.Message, ans.Notes
	}
	return out, nil
}

func (j *job) assess(ctx context.Context, in assessInput) (assessOutput, error) {
	if j.req.Sensitive() {
		return assessOutput{}, j.fail(errSensitiveTool)
	}
	if !j.req.ProviderConsent {
		return assessOutput{}, j.fail(domain.ErrProviderConsent)
	}
	data := j.promptData(in.Knowledge)
	data.Translation = in.Translation
	rendered, err := j.t.assess.Render(data)
	if err != nil {
		return assessOutput{}, j.fail(err)
	}
	messages := []domain.Message{{Role: domain.RoleUser, Text: rendered.User}}
	req := domain.CompletionRequest{
		Task:     domain.TaskAssess,
		System:   []domain.SystemBlock{{Text: rendered.System, Cacheable: true}},
		Messages: messages,
		Output:   &domain.OutputSchema{Name: "assessment", Schema: prompts.AssessSchema()},
	}
	routed, err := j.t.router.Complete(ctx, j.scope(), j.req.TargetLocale, req)
	s := sent{SystemSHA256: sha(rendered.System), Messages: messages}
	j.disclose(domain.TaskAssess, routed.Attempts, s)
	call := callOutput{
		Provider: routed.Route.Provider, Model: routed.Route.Model, PromptVersion: j.t.assess.ID(),
		Usage: routed.Usage, Cost: routed.Cost, Attempts: routed.Attempts, Sent: s,
	}
	if err != nil {
		if ctx.Err() != nil || !skippable(err) {
			return assessOutput{}, j.fail(err)
		}
		// The assessment is a light signal: without it the job goes on.
		return assessOutput{Skipped: true, Reason: err.Error(), callOutput: call}, nil
	}
	var ans prompts.AssessAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(routed.Text)), &ans); err != nil {
		return assessOutput{Skipped: true, Reason: "the answer is not the requested JSON", callOutput: call}, nil
	}
	ans.Score = max(0, min(1, ans.Score))
	return assessOutput{Score: ans.Score, FormalityOK: ans.FormalityOK, Issues: ans.Issues, callOutput: call}, nil
}

// skippable reports assessment failures the job survives: provider
// failures, no route, the budget. Anything else (a cassette miss, a bug)
// fails the job loudly.
func skippable(err error) bool {
	var pe *domain.ProviderError
	return errors.As(err, &pe) || errors.Is(err, domain.ErrBudgetExceeded) ||
		errors.Is(err, domain.ErrNoRoute) || errors.Is(err, ErrAllRoutesFailed)
}

// promptData fills the prompt from the tool results only.
func (j *job) promptData(k knowledge) prompts.Data {
	d := prompts.Data{
		SourceLocale: j.req.SourceLocale, TargetLocale: j.req.TargetLocale, Source: j.req.Source,
		PluralCategories: k.Context.PluralCategories, OrdinalCategories: k.Context.OrdinalCategories,
		Markup: k.Context.Markup,
	}
	for _, a := range k.Context.Arguments {
		pa := prompts.Argument{Name: a.Name, Type: string(a.Type)}
		if a.Selector != nil {
			pa.Selector, pa.Keys = string(a.Selector.Kind), a.Selector.Keys
		}
		d.Arguments = append(d.Arguments, pa)
	}
	c := k.Context.Context
	d.Context = prompts.Context{Key: c.Key, Namespace: c.Namespace, Description: c.Description, Usages: c.Usages}
	if d.Context.Key == "" {
		d.Context.Key = j.req.Key
	}
	if c.MaxLength != nil {
		d.Context.MaxLength = *c.MaxLength
	}
	for _, n := range c.Neighbours {
		d.Context.Neighbours = append(d.Context.Neighbours, prompts.Neighbour{Key: n.Key, Source: n.Source, Translation: n.Translation})
	}
	for _, m := range k.TM.Matches {
		d.TM = append(d.TM, prompts.TMMatch{Score: m.Score, Source: m.Source, Target: m.Target})
	}
	d.Terms = promptTerms(k.Terms.Hits)
	d.Style = promptStyle(k.Style.Style)
	return d
}

// promptTerms is the glossary as a prompt shows it: the target terms a
// translation may use, then the ones it may not. The linguistic
// reviewer renders the same shape, so one glossary reads the same way
// wherever a model sees it.
func promptTerms(hits []domain.TermHit) []prompts.Term {
	var out []prompts.Term
	for _, h := range hits {
		t := prompts.Term{Source: h.Source.Text, Definition: h.Definition}
		for _, status := range []domain.TermStatus{domain.TermPreferred, domain.TermAdmitted} {
			for _, tt := range h.Targets {
				if tt.Status == status {
					t.Use = append(t.Use, tt.Text)
				}
			}
		}
		for _, tt := range h.Targets {
			if !tt.Status.Usable() {
				t.Avoid = append(t.Avoid, tt.Text)
			}
		}
		out = append(out, t)
	}
	return out
}

// promptStyle is the effective style guide as a prompt shows it.
func promptStyle(g domain.StyleGuide) prompts.Style {
	s := prompts.Style{Formality: g.Formality, Pronoun: g.Pronoun, Tone: g.Tone}
	for _, key := range sortedKeys(g.Punctuation) {
		s.Punctuation = append(s.Punctuation, prompts.KeyValue{Key: key, Value: g.Punctuation[key]})
	}
	for _, r := range g.Rules {
		s.Rules = append(s.Rules, prompts.Rule{Rule: r.Rule, Rationale: r.Rationale, Good: r.Good, Bad: r.Bad})
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
