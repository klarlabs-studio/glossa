package app

import "go.klarlabs.de/glossa/platform/internal/intelligence/domain"

// Default models when a tenant uses Anthropic (RFC 0003 §3.1). They are
// configuration: tenants and projects override routing, and nothing in
// the domain names a model.
const (
	DefaultTranslateModel = "claude-sonnet-5"
	DefaultAssessModel    = "claude-haiku-4-5-20251001"
)

// DefaultRouting routes translate and review to Claude Sonnet 5 and the
// cheap self-assessment to Claude Haiku 4.5, all on the provider named
// "anthropic".
func DefaultRouting() domain.RoutingPolicy {
	sonnet := domain.Route{Provider: "anthropic", Model: DefaultTranslateModel, MaxTokens: 8192, Effort: "medium"}
	return domain.RoutingPolicy{Rules: []domain.RoutingRule{
		{Task: domain.TaskTranslate, Routes: []domain.Route{sonnet}},
		{Task: domain.TaskReview, Routes: []domain.Route{sonnet}},
		{Task: domain.TaskExplain, Routes: []domain.Route{sonnet}},
		{Task: domain.TaskAssess, Routes: []domain.Route{{Provider: "anthropic", Model: DefaultAssessModel, MaxTokens: 1024}}},
	}}
}

// DefaultPrices are Anthropic list prices in USD per million tokens
// (cache reads 0.1×, 5-minute cache writes 1.25× the input price).
// Deployments override them; unpriced models cost 0 and are flagged.
func DefaultPrices() domain.PriceTable {
	return domain.PriceTable{
		domain.PriceKey("anthropic", DefaultTranslateModel): {InputPerMTok: 2, OutputPerMTok: 10, CacheReadPerMTok: 0.2, CacheWritePerMTok: 2.5},
		domain.PriceKey("anthropic", DefaultAssessModel):    {InputPerMTok: 1, OutputPerMTok: 5, CacheReadPerMTok: 0.1, CacheWritePerMTok: 1.25},
	}
}
