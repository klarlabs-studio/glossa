package domain

import (
	"errors"
	"slices"

	mf "go.klarlabs.de/glossa/messageformat"
)

// Action is what the review routing policy does with a suggestion
// (RFC 0003 §3.3).
type Action string

// Actions.
const (
	ActionAutoApprove        Action = "auto_approve"
	ActionApproveRecommended Action = "approve_recommended"
	ActionReviewRequired     Action = "review_required"
)

// ReviewPolicy is a project's mapping from confidence to action.
type ReviewPolicy struct {
	// AutoApprove enables auto_approve. It is off by default, and the
	// wiring may only enable it for environments whose eligibility
	// policy accepts approved translations.
	AutoApprove    bool    `json:"auto_approve"`
	AutoApproveMin float64 `json:"auto_approve_min"`
	RecommendMin   float64 `json:"recommend_min"`
	// ForceReview lists factors that require review whatever the score.
	ForceReview []string `json:"force_review,omitempty"`
}

// DefaultReviewPolicy returns the M2 default: no auto-approval; forbidden
// terms and max_length violations always need a human.
func DefaultReviewPolicy() ReviewPolicy {
	return ReviewPolicy{
		AutoApproveMin: 0.92,
		RecommendMin:   0.75,
		ForceReview:    []string{FactorTermForbidden, FactorMaxLength},
	}
}

// MandatoryReview lists factors that require review under every policy:
// a translation lacking the target locale's plural categories is kept
// as a suggestion but never approved without a human.
var MandatoryReview = []string{FactorMissingPluralCategories}

// Route maps a confidence to an action.
func (p ReviewPolicy) Route(c Confidence) Action {
	for _, f := range slices.Concat(MandatoryReview, p.ForceReview) {
		if c.Has(f) {
			return ActionReviewRequired
		}
	}
	switch {
	case p.AutoApprove && c.Score >= p.AutoApproveMin:
		return ActionAutoApprove
	case c.Score >= p.RecommendMin:
		return ActionApproveRecommended
	default:
		return ActionReviewRequired
	}
}

// Provenance records how a suggestion was made (intent §22, RFC 0003
// §3.4): it becomes the revision's origin_detail.
type Provenance struct {
	Origin        Origin   `json:"origin"`
	Provider      string   `json:"provider,omitempty"`
	Model         string   `json:"model,omitempty"`
	PromptVersion string   `json:"prompt_version,omitempty"`
	TMUnitIDs     []string `json:"tm_unit_ids,omitempty"`
	TermIDs       []string `json:"term_ids,omitempty"`
	StyleVersion  string   `json:"style_version,omitempty"`
	Repairs       int      `json:"repairs"`
}

// Call is one priced model call a job made.
type Call struct {
	Task          Task     `json:"task"`
	Provider      string   `json:"provider"`
	Model         string   `json:"model"`
	PromptVersion string   `json:"prompt_version"`
	Usage         Usage    `json:"usage"`
	Cost          MicroUSD `json:"cost"`
}

// Suggestion is the result of a translation job: a structurally valid
// translation with its provenance, confidence and routed action. It is
// stored separately and becomes a revision according to the policy.
type Suggestion struct {
	MessageID    string `json:"message_id"`
	TargetLocale string `json:"target_locale"`
	// Message is the translation in canonical MF2 syntax; Model is its
	// data model.
	Message string     `json:"message"`
	Model   mf.Message `json:"model"`
	// Findings are the structural warnings; TermFindings the
	// terminology QA results. Errors never reach a suggestion.
	Findings     []mf.Finding  `json:"findings,omitempty"`
	TermFindings []TermFinding `json:"term_findings,omitempty"`
	Provenance   Provenance    `json:"provenance"`
	Confidence   Confidence    `json:"confidence"`
	Action       Action        `json:"action"`
	Calls        []Call        `json:"calls,omitempty"`
	Usage        Usage         `json:"usage"`
	Cost         MicroUSD      `json:"cost"`
}

// Job failures.
var (
	// ErrInvalidOutput: the model's draft stayed structurally invalid after
	// the allowed repairs. It never becomes a translation.
	ErrInvalidOutput = errors.New("intelligence: invalid_output")
	// ErrSensitive: the message is in a namespace tagged sensitive; it is
	// never sent to a provider and always needs humans.
	ErrSensitive = errors.New("intelligence: sensitive message needs a human translator")
	// ErrProviderConsent: the tenant has not allowed sending text to AI
	// providers, and no exact translation-memory match could be reused.
	ErrProviderConsent = errors.New("intelligence: sending text to AI providers is not enabled")
)

// TranslationRequest is one message to translate into one locale.
type TranslationRequest struct {
	Scope          Scope  `json:"scope"`
	MessageID      string `json:"message_id"`
	Key            string `json:"key"`
	Namespace      string `json:"namespace,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	SourceLocale   string `json:"source_locale"`
	TargetLocale   string `json:"target_locale"`
	// Source is the source message in MF2 syntax.
	Source string `json:"source"`
	// Tags are the namespace's policy tags (sensitive, legal, marketing).
	Tags []string `json:"tags,omitempty"`
	// ProviderConsent is the tenant's explicit permission to send text to
	// AI providers (RFC 0003 §7). Off by default.
	ProviderConsent bool `json:"provider_consent"`
}

// Validate checks the request is complete.
func (r TranslationRequest) Validate() error {
	switch {
	case r.Scope.TenantID == "":
		return errors.New("translation request: tenant is required")
	case r.MessageID == "":
		return errors.New("translation request: message id is required")
	case r.SourceLocale == "" || r.TargetLocale == "":
		return errors.New("translation request: source and target locale are required")
	case r.Source == "":
		return errors.New("translation request: source is required")
	}
	return nil
}

// Sensitive reports whether the message must never reach a provider.
func (r TranslationRequest) Sensitive() bool { return slices.Contains(r.Tags, TagSensitive) }
