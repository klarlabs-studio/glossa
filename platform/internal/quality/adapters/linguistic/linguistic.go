// Package linguistic is the Quality context's adapter onto
// Intelligence: it runs the linguistic layer's model call and hands
// back the layer (RFC 0005 §3.8).
//
// The layer's rules live in `quality/layers` (the codes, the advisory
// severity, the identity of a finding); the model machinery lives in
// `intelligence` (the provider port, routing, the per-tenant budget,
// the versioned prompt, the constrained answer). This package is the
// seam, and it is the only place the two contexts meet: Quality names
// the vocabulary, Intelligence spends the money, and what comes back is
// a `domain.Finding` like every other layer's.
//
// It is the seam in the other direction too. A linguistic finding is
// computed by a job and only reported by a check (RFC 0005 §14
// decision 2), so nothing in `glossa check` reaches this package — the
// CLI has no path to it, and a caller that wanted one would have to
// import Intelligence into the command, which is the thing the decision
// forbids.
package linguistic

import (
	"context"
	"fmt"

	intel "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// Codes is the linguistic layer's vocabulary as the reviewer needs it.
// There is one list, in the layer, and this translates it: a code the
// layer does not know can never be asked for, rendered into a prompt or
// accepted back.
func Codes() []intel.ReviewCode {
	out := make([]intel.ReviewCode, 0, len(layers.LinguisticCodes))
	for _, c := range layers.LinguisticCodes {
		out = append(out, intel.ReviewCode{Code: c.Code, Meaning: c.Meaning})
	}
	return out
}

// Reviewer runs the linguistic layer for one translation at a time.
type Reviewer struct {
	r *intel.Reviewer
}

// New builds a Reviewer over an Intelligence router. The router carries
// the tenant's providers, routing policy, prices and — the part this
// layer is bounded by — its budget guard (RFC 0005 §10). There is no
// budget here and no second cap: the call is checked against the
// tenant's AI budget before it runs and recorded against it after, like
// every other model call Glossa makes.
func New(router *intel.Router) (*Reviewer, error) {
	r, err := intel.NewReviewer(intel.ReviewConfig{Router: router, Codes: Codes()})
	if err != nil {
		return nil, err
	}
	return &Reviewer{r: r}, nil
}

// PromptVersion is the prompt the reviewer is pinned to, recorded on
// every finding it produces.
func (v *Reviewer) PromptVersion() string { return v.r.PromptVersion() }

// Request is one translation to review. It is Quality's shape, not
// Intelligence's: the caller is the linguistic-QA job, which knows a
// message, a locale and a revision, and should not have to know what a
// routing scope is.
type Request struct {
	// Tenant and Project scope the call, the knowledge and the budget.
	Tenant, Project string
	// Message is the catalog message's ID. It is required: the
	// fingerprint is hashed over it, and a finding minted over a key
	// would not be the one every other surface computes (RFC 0005 §2.1).
	Message   string
	Key       string
	Namespace string
	// Revision is the translation revision under review; SourceRevision
	// the source revision it was made against, which is what a waiver
	// dies with.
	Revision       string
	SourceRevision *int
	SourceLocale   string
	TargetLocale   string
	// Source and Translation are MF2 syntax.
	Source      string
	Translation string
	Description string
	// Terms and Style are the knowledge the job fetched, as evidence.
	Terms []inteldomain.TermHit
	Style *inteldomain.StyleGuide
	// Tags are the namespace's policy tags. A `sensitive` one never
	// reaches a provider (RFC 0003 §7), here exactly as in translation.
	Tags []string
	// ProviderConsent is the tenant's explicit permission to send text
	// to AI providers. Off by default.
	ProviderConsent bool
}

// Result is what one review produced.
type Result struct {
	// Layer is the sealed layer: warnings, fingerprinted, ready to
	// store and to report beside every other layer's findings.
	Layer layers.Linguistic
	// Reviewed are the notes as they were accepted, before sealing, for
	// a caller that wants to record the raw answer.
	Reviewed []layers.Reviewed
	// PromptVersion, Provider and Model are what produced the opinion;
	// they are on every finding's evidence as well.
	PromptVersion, Provider, Model string
	Usage                          inteldomain.Usage
	Cost                           inteldomain.MicroUSD
	// Disclosures record which provider saw the text (RFC 0003 §7).
	Disclosures []intel.Disclosure
}

// Findings is the layer's findings.
func (r Result) Findings() []domain.Finding { return r.Layer.Findings }

// Review reviews one translation and returns the layer.
//
// The errors a caller has to tell apart are Intelligence's own and
// unchanged: domain.ErrSensitive, domain.ErrProviderConsent,
// domain.ErrBudgetExceeded and domain.ErrMalformedReview (which wraps
// domain.ErrInvalidOutput). A malformed answer produces no findings at
// all — not a partial layer — because half a review is not evidence.
func (v *Reviewer) Review(ctx context.Context, req Request) (Result, error) {
	if req.Message == "" {
		return Result{}, fmt.Errorf("linguistic: the message ID is required, because the finding's fingerprint is hashed over it")
	}
	res, err := v.r.Review(ctx, inteldomain.ReviewRequest{
		Scope:        inteldomain.Scope{TenantID: req.Tenant, ProjectID: req.Project},
		MessageID:    req.Message,
		Key:          req.Key,
		Namespace:    req.Namespace,
		Revision:     req.Revision,
		SourceLocale: req.SourceLocale,
		TargetLocale: req.TargetLocale,
		Source:       req.Source,
		Translation:  req.Translation,
		Description:  req.Description,
		Terms:        req.Terms,
		Style:        req.Style,
		Tags:         req.Tags,

		ProviderConsent: req.ProviderConsent,
	})
	out := Result{
		PromptVersion: res.PromptVersion, Provider: res.Provider, Model: res.Model,
		Usage: res.Usage, Cost: res.Cost, Disclosures: res.Disclosures,
	}
	if err != nil {
		return out, err
	}
	out.Reviewed = reviewed(req, res)
	out.Layer = layers.NewLinguistic(out.Reviewed)
	return out, nil
}

// reviewed turns the accepted notes into the layer's input: the locus
// the job knows, plus the span the reviewer resolved and the evidence
// that says which prompt and model produced the opinion.
func reviewed(req Request, res intel.ReviewResult) []layers.Reviewed {
	out := make([]layers.Reviewed, 0, len(res.Notes))
	for _, n := range res.Notes {
		locus := domain.Locus{
			Message:   req.Message,
			Key:       req.Key,
			Locale:    req.TargetLocale,
			Namespace: req.Namespace,
			Revision:  req.Revision,
			Span:      &domain.Span{Side: domain.Side(n.Side), Start: n.Start, End: n.End},
		}
		out = append(out, layers.Reviewed{
			Locus:          locus,
			Code:           n.Code,
			Quote:          n.Quote,
			Explanation:    n.Explanation,
			Suggestion:     n.Suggestion,
			SourceRevision: req.SourceRevision,
			Evidence: map[string]any{
				layers.EvidencePromptVersion: res.PromptVersion,
				layers.EvidenceProvider:      res.Provider,
				layers.EvidenceModel:         res.Model,
			},
		})
	}
	return out
}
