package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	agentapi "go.klarlabs.de/agent/interfaces/api"
	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/intelligence/prompts"
)

// PromptVersions pins the prompt templates a Translator uses.
type PromptVersions struct {
	Translate, Repair, Assess string
}

// DefaultPromptVersions are the current prompts.
func DefaultPromptVersions() PromptVersions {
	return PromptVersions{Translate: "v1", Repair: "v1", Assess: "v1"}
}

// MaxRepairs is RFC 0003's limit on structural repairs.
const MaxRepairs = 2

// Config configures a Translator.
type Config struct {
	Router    *Router
	Knowledge domain.Knowledge
	Scoring   domain.ScoringConfig
	Review    domain.ReviewPolicy
	Prompts   PromptVersions
	// MaxRepairs is at most MaxRepairs (the default).
	MaxRepairs int
	// SkipAssessment turns the model self-assessment off.
	SkipAssessment bool
	// TMLimit caps the TM matches shown to the model (default 3).
	TMLimit int
	// ToolTimeout bounds one tool execution, model calls with their
	// retries included (default 5m).
	ToolTimeout time.Duration
}

// Translator runs the translation agent for one message and locale at a
// time. It is safe for concurrent use: every job gets its own agent-go
// engine and axi-go toolset.
type Translator struct {
	cfg                       Config
	router                    *Router
	translate, repair, assess *prompts.Template
}

// NewTranslator validates cfg and loads the pinned prompts.
func NewTranslator(cfg Config) (*Translator, error) {
	if cfg.Router == nil || cfg.Knowledge == nil {
		return nil, errors.New("translator: a router and knowledge are required")
	}
	if cfg.MaxRepairs <= 0 || cfg.MaxRepairs > MaxRepairs {
		cfg.MaxRepairs = MaxRepairs
	}
	if cfg.TMLimit <= 0 {
		cfg.TMLimit = 3
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = 5 * time.Minute
	}
	if cfg.Prompts == (PromptVersions{}) {
		cfg.Prompts = DefaultPromptVersions()
	}
	if cfg.Scoring.OriginRisk == nil {
		cfg.Scoring = domain.DefaultScoring()
	}
	if cfg.Review.RecommendMin == 0 {
		cfg.Review = domain.DefaultReviewPolicy()
	}
	t := &Translator{cfg: cfg, router: cfg.Router}
	var err error
	if t.translate, err = prompts.Load(prompts.Translate, cfg.Prompts.Translate); err != nil {
		return nil, err
	}
	if t.repair, err = prompts.Load(prompts.Repair, cfg.Prompts.Repair); err != nil {
		return nil, err
	}
	if t.assess, err = prompts.Load(prompts.Assess, cfg.Prompts.Assess); err != nil {
		return nil, err
	}
	return t, nil
}

// AuditEntry is one tool result of a run, in order: the agent's ledger
// of what it looked up, what it sent and what came back.
type AuditEntry struct {
	Tool   string          `json:"tool"`
	Output json.RawMessage `json:"output"`
	At     time.Time       `json:"at"`
}

// Result is a job's outcome. Disclosures and Audit are filled whether or
// not the job succeeded: a provider that saw the text is recorded even
// when its answer was unusable.
type Result struct {
	Suggestion  domain.Suggestion `json:"suggestion"`
	Disclosures []Disclosure      `json:"disclosures,omitempty"`
	Audit       []AuditEntry      `json:"audit,omitempty"`
	// Findings are the last structural errors of an invalid_output job.
	Findings []mf.Finding `json:"findings,omitempty"`
}

// Translate runs the agent: gather → draft → validate → repair (≤ 2) →
// assess → done. It returns ErrSensitive, ErrProviderConsent or
// ErrInvalidOutput (wrapped) for the outcomes that never produce a
// suggestion, or the provider/budget error that stopped the job.
func (t *Translator) Translate(ctx context.Context, req domain.TranslationRequest) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if req.Sensitive() {
		return Result{}, domain.ErrSensitive
	}
	source, _, err := domain.ParseMessage(req.Source)
	if err != nil {
		return Result{}, fmt.Errorf("translator: source message: %w", err)
	}
	j := &job{req: req, source: source, t: t}
	tools, err := newTools(j)
	if err != nil {
		return Result{}, fmt.Errorf("translator: toolset: %w", err)
	}
	opts := []agentapi.Option{
		agentapi.WithPlanner(&planner{consent: req.ProviderConsent, maxRepairs: t.cfg.MaxRepairs, assess: !t.cfg.SkipAssessment}),
		agentapi.WithToolEligibility(agentapi.NewToolEligibilityWith(eligibility())),
		agentapi.WithExecutor(agentapi.NewExecutorWithOptions(agentapi.WithExecutorTimeout(t.cfg.ToolTimeout))),
		agentapi.WithMaxSteps(64),
		agentapi.WithBudget("tool_calls", 24),
	}
	for _, tl := range tools {
		opts = append(opts, agentapi.WithTool(tl))
	}
	engine, err := agentapi.New(opts...)
	if err != nil {
		return Result{}, fmt.Errorf("translator: agent: %w", err)
	}
	run, runErr := engine.Run(ctx, fmt.Sprintf("Translate message %s from %s to %s", req.MessageID, req.SourceLocale, req.TargetLocale))

	res := Result{Disclosures: j.disc}
	if run != nil {
		for _, e := range run.Evidence {
			res.Audit = append(res.Audit, AuditEntry{Tool: e.Source, Output: e.Content, At: e.Timestamp})
		}
	}
	if runErr != nil {
		if j.toolErr != nil {
			return res, j.toolErr
		}
		return res, fmt.Errorf("translator: agent run: %w", runErr)
	}
	tr, err := readTrail(run.Evidence)
	if err != nil {
		return res, err
	}
	switch run.Status {
	case agentapi.StatusCompleted:
	case agentapi.StatusFailed:
		switch run.Error {
		case reasonProviderConsent:
			return res, domain.ErrProviderConsent
		case reasonInvalidOutput:
			if last := tr.lastCheck(); last != nil {
				res.Findings = last.Structure.Errors
			}
			return res, fmt.Errorf("%w: the draft stayed structurally invalid after %d repairs", domain.ErrInvalidOutput, t.cfg.MaxRepairs)
		}
		return res, fmt.Errorf("translator: agent failed: %s", run.Error)
	default:
		return res, fmt.Errorf("translator: agent stopped in status %s", run.Status)
	}
	var finished struct {
		Origin domain.Origin `json:"origin"`
	}
	if err := json.Unmarshal(run.Result, &finished); err != nil {
		return res, fmt.Errorf("translator: agent result: %w", err)
	}
	s, err := t.suggestion(req, source, tr, finished.Origin)
	if err != nil {
		return res, err
	}
	res.Suggestion = s
	return res, nil
}

// suggestion builds the result from the run's evidence.
func (t *Translator) suggestion(req domain.TranslationRequest, source mf.Message, tr trail, origin domain.Origin) (domain.Suggestion, error) {
	check := tr.lastCheck()
	if origin == domain.OriginTranslationMemory {
		check = tr.tmCheck
	}
	if check == nil || !check.Valid {
		return domain.Suggestion{}, errors.New("translator: finished without a valid translation") // the planner never does this
	}
	model, canonical, err := domain.ParseMessage(check.Structure.Canonical)
	if err != nil {
		return domain.Suggestion{}, err
	}
	s := domain.Suggestion{
		MessageID: req.MessageID, TargetLocale: req.TargetLocale,
		Message: canonical, Model: model,
		Findings: check.Structure.Warnings, TermFindings: check.TermFindings,
		Provenance: domain.Provenance{Origin: origin, Repairs: max(len(tr.drafts)-1, 0)},
	}
	k := tr.knowledge()
	s.Provenance.StyleVersion = k.Style.Style.Version
	for _, h := range k.Terms.Hits {
		s.Provenance.TermIDs = appendUnique(s.Provenance.TermIDs, h.Source.ID)
		for _, tt := range h.Targets {
			s.Provenance.TermIDs = appendUnique(s.Provenance.TermIDs, tt.ID)
		}
	}
	bestTM := 0
	for _, m := range k.TM.Matches {
		bestTM = max(bestTM, m.Score)
	}
	if origin == domain.OriginTranslationMemory {
		s.Provenance.TMUnitIDs = []string{check.UnitID}
		bestTM = scoreOf(k.TM.Matches, check.UnitID)
	} else {
		for _, m := range k.TM.Matches[:min(len(k.TM.Matches), t.cfg.TMLimit)] {
			s.Provenance.TMUnitIDs = append(s.Provenance.TMUnitIDs, m.UnitID)
		}
		last := tr.drafts[len(tr.drafts)-1]
		s.Provenance.Provider, s.Provenance.Model, s.Provenance.PromptVersion = last.Provider, last.Model, last.PromptVersion
	}
	for _, d := range tr.drafts {
		s.Calls = append(s.Calls, domain.Call{Task: domain.TaskTranslate, Provider: d.Provider, Model: d.Model, PromptVersion: d.PromptVersion, Usage: d.Usage, Cost: d.Cost})
	}
	signals := domain.Signals{
		Origin: origin, TMScore: bestTM, Repairs: s.Provenance.Repairs,
		QAWarnings:   countWarnings(check.Structure.Warnings),
		SourceLength: domain.TextLength(source), TextLength: check.Structure.TextLength,
		MaxLength: tr.maxLength(), SourceLocale: req.SourceLocale, TargetLocale: req.TargetLocale,
		Tags: slices.Concat(req.Tags, k.Context.Context.Tags), MarkupCount: domain.MarkupCount(source),
		// Still missing after the allowed repairs: kept, but forced to review.
		MissingPluralCategories: check.Structure.MissingPluralCategories(),
	}
	for _, f := range check.TermFindings {
		switch f.Code {
		case domain.FindingTermMissing:
			signals.TermMissing++
		case domain.FindingTermForbidden:
			signals.TermForbidden++
		}
	}
	if a := tr.assessed; a != nil && !a.Skipped {
		score, formality := a.Score, a.FormalityOK
		signals.SelfAssessment, signals.FormalityOK = &score, &formality
	}
	if a := tr.assessed; a != nil && a.Provider != "" {
		s.Calls = append(s.Calls, domain.Call{Task: domain.TaskAssess, Provider: a.Provider, Model: a.Model, PromptVersion: a.PromptVersion, Usage: a.Usage, Cost: a.Cost})
	}
	for _, c := range s.Calls {
		s.Usage = s.Usage.Add(c.Usage)
		s.Cost += c.Cost
	}
	s.Confidence = t.cfg.Scoring.Score(signals)
	s.Action = t.cfg.Review.Route(s.Confidence)
	return s, nil
}

// countWarnings counts structural warnings; max_length and missing
// plural categories have their own factors, so they are not counted
// twice.
func countWarnings(fs []mf.Finding) int {
	n := 0
	for _, f := range fs {
		if f.Code != FindingMaxLengthExceeded && f.Code != mf.FindingMissingPluralCategory {
			n++
		}
	}
	return n
}

func scoreOf(matches []domain.TMMatch, unit string) int {
	for _, m := range matches {
		if m.UnitID == unit {
			return m.Score
		}
	}
	return 0
}

func appendUnique(list []string, s string) []string {
	if s == "" || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
