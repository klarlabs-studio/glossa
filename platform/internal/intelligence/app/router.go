// Package app is the Intelligence context's application layer: the
// translation agent (agent-go driving an axi-go toolset), the provider
// router, validation and the Translator entry point.
package app

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Router runs a model call on the routes the policy gives for its task
// and locale, in order, falling back to the next route when a provider
// fails (RFC 0003 §3.1). Every call is budget-checked before it runs and
// recorded after, with the cost from the price table.
type Router struct {
	providers map[string]domain.Provider
	policy    domain.RoutingPolicy
	prices    domain.PriceTable
	budget    domain.BudgetGuard
}

// NewRouter returns a router. providers are keyed by name and should be
// wrapped for resilience (adapters/resilient).
func NewRouter(providers map[string]domain.Provider, policy domain.RoutingPolicy, prices domain.PriceTable, budget domain.BudgetGuard) *Router {
	if budget == nil {
		budget = domain.NoBudget{}
	}
	return &Router{providers: providers, policy: policy, prices: prices, budget: budget}
}

// Attempt is one route the router tried. A provider that was tried saw
// the prompt, whether or not it answered (RFC 0003 §7).
type Attempt struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Error    string `json:"error,omitempty"`
}

// Routed is a successful routed call.
type Routed struct {
	domain.Completion
	Route    domain.Route
	Cost     domain.MicroUSD
	Priced   bool
	Attempts []Attempt
}

// ErrAllRoutesFailed wraps the last provider error when no route answered.
var ErrAllRoutesFailed = errors.New("intelligence: every provider route failed")

// Complete runs req (whose model and limits the route sets) for task and
// target locale. On failure the returned Routed still lists the attempts.
func (r *Router) Complete(ctx context.Context, scope domain.Scope, locale string, req domain.CompletionRequest) (Routed, error) {
	routes, err := r.policy.Resolve(req.Task, locale)
	if err != nil {
		return Routed{}, err
	}
	var (
		attempts []Attempt
		errs     []error
	)
	for _, route := range routes {
		p, ok := r.providers[route.Provider]
		if !ok {
			err := fmt.Errorf("route names unconfigured provider %q", route.Provider)
			errs = append(errs, err)
			continue
		}
		call := req
		call.Model, call.MaxTokens, call.Temperature, call.Effort = route.Model, route.MaxTokens, route.Temperature, route.Effort
		estimate := r.prices.Estimate(route.Provider, route.Model, estimateTokens(call), call.MaxTokens)
		if err := r.budget.Check(ctx, scope, estimate); err != nil {
			return Routed{Attempts: attempts}, err
		}
		attempts = append(attempts, Attempt{Provider: route.Provider, Model: route.Model})
		out, err := p.Complete(ctx, call)
		cost, priced := r.prices.Cost(route.Provider, route.Model, out.Usage)
		if out.Usage != (domain.Usage{}) {
			// Failed answers that consumed tokens were billed too.
			if recErr := r.budget.Record(ctx, scope, domain.Spend{Task: req.Task, Provider: route.Provider, Model: route.Model, Usage: out.Usage, Cost: cost}); recErr != nil {
				return Routed{Attempts: attempts}, fmt.Errorf("record spend: %w", recErr)
			}
		}
		if err == nil {
			return Routed{Completion: out, Route: route, Cost: cost, Priced: priced, Attempts: attempts}, nil
		}
		attempts[len(attempts)-1].Error = err.Error()
		var pe *domain.ProviderError
		if !errors.As(err, &pe) {
			// Not a provider answer (cancellation, a cassette miss): stop.
			return Routed{Attempts: attempts}, err
		}
		errs = append(errs, err)
	}
	return Routed{Attempts: attempts}, fmt.Errorf("%w for %s/%s: %w", ErrAllRoutesFailed, req.Task, locale, errors.Join(errs...))
}

// Transient reports whether a failed job may succeed if run again later:
// some route failed with a retryable outage (rate limit, overload), not
// only with rejections. Job workers use it to requeue instead of failing.
func Transient(err error) bool {
	if err == nil {
		return false
	}
	if pe, ok := err.(*domain.ProviderError); ok {
		return pe.Retryable()
	}
	switch u := err.(type) {
	case interface{ Unwrap() []error }:
		for _, inner := range u.Unwrap() {
			if Transient(inner) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return Transient(u.Unwrap())
	}
	return false
}

// estimateTokens is a deliberately generous input-token estimate for the
// budget pre-check: about three characters per token.
func estimateTokens(req domain.CompletionRequest) int {
	n := 0
	for _, s := range req.System {
		n += utf8.RuneCountInString(s.Text)
	}
	for _, m := range req.Messages {
		n += utf8.RuneCountInString(m.Text)
	}
	return n/3 + 1
}
