package domain_test

import (
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

func TestRoutingPolicyResolve(t *testing.T) {
	sonnet := domain.Route{Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 8000}
	haiku := domain.Route{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", MaxTokens: 1024}
	local := domain.Route{Provider: "local", Model: "llama", MaxTokens: 2000}
	gemini := domain.Route{Provider: "gemini", Model: "gemini-pro", MaxTokens: 4000}
	policy := domain.RoutingPolicy{Rules: []domain.RoutingRule{
		{Task: domain.TaskTranslate, Routes: []domain.Route{sonnet, local}},
		{Task: domain.TaskTranslate, Locales: []string{"ja"}, Routes: []domain.Route{gemini, sonnet}},
		{Task: domain.TaskTranslate, Locales: []string{"ja-JP"}, Routes: []domain.Route{local}},
		{Task: domain.TaskAssess, Routes: []domain.Route{haiku}},
		{Task: domain.TaskReview, Routes: nil},
	}}
	tests := []struct {
		name   string
		task   domain.Task
		locale string
		want   []domain.Route
		err    error
	}{
		{name: "catch-all keeps order", task: domain.TaskTranslate, locale: "de", want: []domain.Route{sonnet, local}},
		{name: "language beats catch-all", task: domain.TaskTranslate, locale: "ja-Hira", want: []domain.Route{gemini, sonnet}},
		{name: "language rule matches the bare language", task: domain.TaskTranslate, locale: "ja", want: []domain.Route{gemini, sonnet}},
		{name: "exact beats language", task: domain.TaskTranslate, locale: "ja-JP", want: []domain.Route{local}},
		{name: "per task", task: domain.TaskAssess, locale: "fr", want: []domain.Route{haiku}},
		{name: "rule without routes is no route", task: domain.TaskReview, locale: "fr", err: domain.ErrNoRoute},
		{name: "unknown task", task: domain.TaskExplain, locale: "fr", err: domain.ErrNoRoute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := policy.Resolve(tc.task, tc.locale)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("routes = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i].Provider != tc.want[i].Provider || got[i].Model != tc.want[i].Model {
					t.Errorf("route %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestPriceTableCost(t *testing.T) {
	table := domain.PriceTable{
		domain.PriceKey("anthropic", "claude-sonnet-5"): {InputPerMTok: 2, OutputPerMTok: 10, CacheReadPerMTok: 0.2, CacheWritePerMTok: 2.5},
	}
	tests := []struct {
		name  string
		model string
		usage domain.Usage
		want  domain.MicroUSD
		ok    bool
	}{
		{name: "input and output", model: "claude-sonnet-5", usage: domain.Usage{InputTokens: 1000, OutputTokens: 500}, want: 7000, ok: true},
		{name: "cache tokens", model: "claude-sonnet-5", usage: domain.Usage{CacheReadTokens: 1000, CacheWriteTokens: 1000}, want: 2700, ok: true},
		{name: "unpriced model", model: "llama", usage: domain.Usage{InputTokens: 1000}, want: 0, ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := table.Cost("anthropic", tc.model, tc.usage)
			if got != tc.want || ok != tc.ok {
				t.Errorf("Cost = %v,%v want %v,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
	if got := table.Estimate("anthropic", "claude-sonnet-5", 1000, 1000); got != 12000 {
		t.Errorf("Estimate = %v, want 12000", got)
	}
	if got := domain.MicroUSD(12000).String(); got != "$0.012000" {
		t.Errorf("String = %q", got)
	}
}

func TestProviderErrorRetryable(t *testing.T) {
	tests := []struct {
		kind domain.ErrorKind
		want bool
	}{
		{domain.KindRateLimited, true},
		{domain.KindUnavailable, true},
		{domain.KindInvalidRequest, false},
		{domain.KindAuth, false},
		{domain.KindRefused, false},
		{domain.KindTruncated, false},
		{domain.KindBadResponse, false},
	}
	for _, tc := range tests {
		err := error(&domain.ProviderError{Provider: "p", Kind: tc.kind, Status: 500, Err: errors.New("boom")})
		if got := domain.IsRetryable(err); got != tc.want {
			t.Errorf("%s: retryable = %v, want %v", tc.kind, got, tc.want)
		}
	}
	if domain.IsRetryable(errors.New("plain")) {
		t.Error("a plain error is not retryable")
	}
	msg := (&domain.ProviderError{Provider: "anthropic", Kind: domain.KindUnavailable, Status: 529, Err: errors.New("overloaded")}).Error()
	if msg != "provider anthropic: unavailable (HTTP 529): overloaded" {
		t.Errorf("Error() = %q", msg)
	}
}

func TestCompletionRequestValidate(t *testing.T) {
	ok := domain.CompletionRequest{Model: "m", MaxTokens: 10, Messages: []domain.Message{{Role: domain.RoleUser, Text: "hi"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*domain.CompletionRequest){
		"no model":      func(r *domain.CompletionRequest) { r.Model = "" },
		"no max tokens": func(r *domain.CompletionRequest) { r.MaxTokens = 0 },
		"no messages":   func(r *domain.CompletionRequest) { r.Messages = nil },
	} {
		r := ok
		mut(&r)
		if r.Validate() == nil {
			t.Errorf("%s: want error", name)
		}
	}
	sum := domain.Usage{InputTokens: 1, OutputTokens: 2}.Add(domain.Usage{InputTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5})
	if sum != (domain.Usage{InputTokens: 4, OutputTokens: 2, CacheReadTokens: 4, CacheWriteTokens: 5}) {
		t.Errorf("Add = %+v", sum)
	}
}
