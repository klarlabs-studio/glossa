// Package intelligence adapts Intelligence to Quality's Linguist port:
// the linguistic layer is the one layer that needs a model, and the
// model call belongs to Intelligence (RFC 0002 §4, contexts reach each
// other through application ports).
//
// What this package is *for* is the preflight. RFC 0005 §3.8 says the
// linguistic layer "inherits M2's rules wholesale", and inheriting them
// means asking the context that owns them rather than writing them
// again:
//
//   - **Sending consent** is `intelligence_settings.provider_consent`,
//     off by default and read here through GetSettings — the same value
//     the translation agent's every call is gated on (RFC 0003 §7).
//   - **The budget** is the tenant's existing monthly AI budget, read
//     here as `GetBudget().Remaining()`. It is the same figure
//     Intelligence's own BudgetGuard checks before each provider call,
//     so the linguistic layer is bounded by it exactly as translation
//     is (RFC 0005 §10) — and Intelligence checks it again on the call
//     itself, because a gate only at the door is a gate the second door
//     does not have.
//   - **The `sensitive` namespace rule** is the project's namespace
//     tags, read here through GetProjectSettings. Quality excludes
//     those namespaces from the request it hands over, so a message
//     under one is never offered to a provider (RFC 0003 §7).
//
// Every call is an ordinary authorized Intelligence use case, so
// Quality learns nothing its caller could not have read through the
// API, and Intelligence keeps checking the caller's permissions.
//
// The **review itself** — the prompt and its version, the structurally
// constrained output, the cassettes and the golden set (RFC 0003 §3.1,
// §3.2, §4) — is behind Reviewer, which the wave-6 layer slice
// implements. Nothing about a model lives in this file.
package intelligence

import (
	"context"

	"github.com/google/uuid"

	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	intelligencedomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

// Reviewer is the linguistic layer itself: it runs one batch of
// translations past a model and parses the constrained answer into
// findings. It is the other wave-6 slice's (RFC 0005 §13) — this slice
// owns the job, that one owns the review — and the two meet exactly
// here.
//
// A finding it returns carries no severity, by construction: see
// quality/domain.LinguisticFinding. What a model suspects is graded by
// the check policy, and the policy may never make it an error.
type Reviewer interface {
	// Start runs or queues one review and returns a handle for it.
	Start(ctx context.Context, req app.LinguisticRequest) (batch string, err error)
	// Poll reports where the review stands.
	Poll(ctx context.Context, project uuid.UUID, batch string) (app.LinguisticProgress, error)
	// Cancel stops a review that has not finished.
	Cancel(ctx context.Context, project uuid.UUID, batch string) error
}

// Port implements app.Linguist.
type Port struct {
	svc      *intelligenceapp.Service
	reviewer Reviewer
}

var _ app.Linguist = (*Port)(nil)

// New returns the port, or nil where the deployment has no reviewer.
//
// A nil Linguist is not a Quality that reports every project as clean:
// it is one that refuses a review with `linguistic_unavailable`,
// because "no model looked" and "a model looked and found nothing" are
// different answers and only one of them is a clean bill of health.
func New(svc *intelligenceapp.Service, reviewer Reviewer) app.Linguist {
	if svc == nil || reviewer == nil {
		return nil
	}
	return &Port{svc: svc, reviewer: reviewer}
}

// Preflight implements app.Linguist with Intelligence's own settings,
// budget and namespace tags. Nothing is recomputed here; the three
// answers are read from the context that owns them.
func (p *Port) Preflight(ctx context.Context, project uuid.UUID) (app.LinguisticPreflight, error) {
	settings, err := p.svc.GetSettings(ctx)
	if err != nil {
		return app.LinguisticPreflight{}, err
	}
	budget, err := p.svc.GetBudget(ctx)
	if err != nil {
		return app.LinguisticPreflight{}, err
	}
	ps, err := p.svc.GetProjectSettings(ctx, project)
	if err != nil {
		return app.LinguisticPreflight{}, err
	}
	out := app.LinguisticPreflight{
		ProviderConsent: settings.ProviderConsent,
		BudgetRemaining: int64(budget.Remaining()),
	}
	for ns := range ps.NamespaceTags {
		for _, tag := range ps.NamespaceTags.Of(ns) {
			if tag == intelligencedomain.TagSensitive {
				out.SensitiveNamespaces = append(out.SensitiveNamespaces, ns)
				break
			}
		}
	}
	return out, nil
}

// Start implements app.Linguist.
func (p *Port) Start(ctx context.Context, req app.LinguisticRequest) (string, error) {
	return p.reviewer.Start(ctx, req)
}

// Poll implements app.Linguist.
func (p *Port) Poll(ctx context.Context, project uuid.UUID, batch string) (app.LinguisticProgress, error) {
	return p.reviewer.Poll(ctx, project, batch)
}

// Cancel implements app.Linguist.
func (p *Port) Cancel(ctx context.Context, project uuid.UUID, batch string) error {
	return p.reviewer.Cancel(ctx, project, batch)
}
