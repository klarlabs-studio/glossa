package domain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Route is one provider and model a task may run on, with its call
// parameters.
type Route struct {
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature *float64 `json:"temperature,omitempty"`
	Effort      string   `json:"effort,omitempty"`
}

// RoutingRule maps a task, optionally limited to target locales, to an
// ordered list of routes: the first is preferred, the rest are fallbacks.
type RoutingRule struct {
	Task Task `json:"task"`
	// Locales limits the rule to these target locales; a language
	// ("pt") matches its regional variants ("pt-BR"). Empty matches any.
	Locales []string `json:"locales,omitempty"`
	Routes  []Route  `json:"routes"`
}

// RoutingPolicy chooses provider and model by task and locale
// (tenant/project scoped; RFC 0003 §3.1). The most specific matching rule
// wins: an exact locale beats a language, which beats a catch-all.
type RoutingPolicy struct {
	Rules []RoutingRule `json:"rules"`
}

// ErrNoRoute means no rule covers a task and locale.
var ErrNoRoute = errors.New("intelligence: no provider route for task and locale")

// Resolve returns the ordered routes for task and target locale.
func (p RoutingPolicy) Resolve(task Task, locale string) ([]Route, error) {
	best, bestRank := -1, -1
	for i, r := range p.Rules {
		if r.Task != task || len(r.Routes) == 0 {
			continue
		}
		if rank := localeRank(r.Locales, locale); rank > bestRank {
			best, bestRank = i, rank
		}
	}
	if best < 0 {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoRoute, task, locale)
	}
	return append([]Route(nil), p.Rules[best].Routes...), nil
}

// localeRank scores how specifically locales matches target: 2 exact,
// 1 language, 0 catch-all, -1 no match.
func localeRank(locales []string, target string) int {
	if len(locales) == 0 {
		return 0
	}
	rank := -1
	for _, l := range locales {
		switch {
		case strings.EqualFold(l, target):
			return 2
		case strings.HasPrefix(strings.ToLower(target), strings.ToLower(l)+"-"):
			rank = 1
		}
	}
	return rank
}

// MicroUSD is money in millionths of a US dollar, integer to avoid drift.
type MicroUSD int64

// USD returns m in dollars.
func (m MicroUSD) USD() float64 { return float64(m) / 1e6 }

func (m MicroUSD) String() string { return fmt.Sprintf("$%.6f", m.USD()) }

// Price is a model's list price in US dollars per million tokens.
type Price struct {
	InputPerMTok      float64 `json:"input_per_mtok"`
	OutputPerMTok     float64 `json:"output_per_mtok"`
	CacheReadPerMTok  float64 `json:"cache_read_per_mtok,omitempty"`
	CacheWritePerMTok float64 `json:"cache_write_per_mtok,omitempty"`
}

// PriceTable holds prices by "provider/model". It is configuration: the
// defaults live with the composition, never in domain logic.
type PriceTable map[string]Price

// PriceKey is the table key of a provider's model.
func PriceKey(provider, model string) string { return provider + "/" + model }

// Cost prices usage; ok is false when the model has no price (cost 0).
func (t PriceTable) Cost(provider, model string, u Usage) (cost MicroUSD, ok bool) {
	p, ok := t[PriceKey(provider, model)]
	if !ok {
		return 0, false
	}
	micros := float64(u.InputTokens)*p.InputPerMTok +
		float64(u.OutputTokens)*p.OutputPerMTok +
		float64(u.CacheReadTokens)*p.CacheReadPerMTok +
		float64(u.CacheWriteTokens)*p.CacheWritePerMTok
	return MicroUSD(math.Round(micros)), true // tokens × $/MTok = µ$
}

// Estimate is an upper bound for a call before it runs: inputTokens at
// the input price and the whole max_tokens budget at the output price.
func (t PriceTable) Estimate(provider, model string, inputTokens, maxTokens int) MicroUSD {
	c, _ := t.Cost(provider, model, Usage{InputTokens: int64(inputTokens), OutputTokens: int64(maxTokens)})
	return c
}

// Scope is the tenant and project a job runs for. Knowledge lookups,
// budgets and audit are scoped by it.
type Scope struct {
	TenantID  string `json:"tenant_id"`
	ProjectID string `json:"project_id,omitempty"`
}

// Spend is one priced model call, for budget accounting.
type Spend struct {
	Task     Task     `json:"task"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Usage    Usage    `json:"usage"`
	Cost     MicroUSD `json:"cost"`
}

// ErrBudgetExceeded means a tenant's AI budget cap would be or has been
// exceeded. It is never retried and never falls back to another route.
var ErrBudgetExceeded = errors.New("intelligence: AI budget exceeded")

// BudgetGuard is the per-tenant budget cap (RFC 0003 §3.1): Check before
// each call with an upper-bound estimate, Record the actual spend after.
type BudgetGuard interface {
	// Check returns an error wrapping ErrBudgetExceeded when estimate
	// would take scope over its cap.
	Check(ctx context.Context, scope Scope, estimate MicroUSD) error
	// Record books the actual cost of a finished call.
	Record(ctx context.Context, scope Scope, spend Spend) error
}

// NoBudget is a BudgetGuard without a cap. Use it only where no tenant
// money is spent (tests, cassette replays).
type NoBudget struct{}

// Check implements BudgetGuard.
func (NoBudget) Check(context.Context, Scope, MicroUSD) error { return nil }

// Record implements BudgetGuard.
func (NoBudget) Record(context.Context, Scope, Spend) error { return nil }
