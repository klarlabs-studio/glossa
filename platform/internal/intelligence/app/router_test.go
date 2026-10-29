package app_test

import (
	"context"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/memory"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

type stubProvider struct {
	name  string
	err   error
	usage domain.Usage
	got   []domain.CompletionRequest
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Complete(_ context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	s.got = append(s.got, req)
	return domain.Completion{Provider: s.name, Model: req.Model, Text: "hi from " + s.name, Usage: s.usage}, s.err
}

func routerFixture(primaryErr error) (*app.Router, *stubProvider, *stubProvider, *memory.Budget) {
	primary := &stubProvider{name: "anthropic", err: primaryErr, usage: domain.Usage{InputTokens: 1000, OutputTokens: 100}}
	secondary := &stubProvider{name: "local", usage: domain.Usage{InputTokens: 10}}
	policy := domain.RoutingPolicy{Rules: []domain.RoutingRule{{
		Task: domain.TaskTranslate,
		Routes: []domain.Route{
			{Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 1000, Effort: "medium"},
			{Provider: "local", Model: "llama", MaxTokens: 500},
		},
	}}}
	prices := domain.PriceTable{domain.PriceKey("anthropic", "claude-sonnet-5"): {InputPerMTok: 2, OutputPerMTok: 10}}
	budget := memory.NewBudget(map[string]domain.MicroUSD{"t1": 1_000_000})
	return app.NewRouter(map[string]domain.Provider{"anthropic": primary, "local": secondary}, policy, prices, budget), primary, secondary, budget
}

var routeReq = domain.CompletionRequest{Task: domain.TaskTranslate, Messages: []domain.Message{{Role: domain.RoleUser, Text: "Translate"}}}

func TestRouterUsesPreferredRoute(t *testing.T) {
	r, primary, secondary, budget := routerFixture(nil)
	out, err := r.Complete(context.Background(), domain.Scope{TenantID: "t1"}, "de", routeReq)
	if err != nil {
		t.Fatal(err)
	}
	if out.Route.Provider != "anthropic" || out.Cost != 3000 || !out.Priced || len(out.Attempts) != 1 || len(secondary.got) != 0 {
		t.Errorf("routed = %+v", out)
	}
	sent := primary.got[0]
	if sent.Model != "claude-sonnet-5" || sent.MaxTokens != 1000 || sent.Effort != "medium" {
		t.Errorf("route parameters not applied: %+v", sent)
	}
	if budget.Spent("t1") != 3000 {
		t.Errorf("spent = %v", budget.Spent("t1"))
	}
}

func TestRouterFallsBackOnProviderFailure(t *testing.T) {
	r, _, _, budget := routerFixture(&domain.ProviderError{Provider: "anthropic", Kind: domain.KindUnavailable, Status: 529})
	out, err := r.Complete(context.Background(), domain.Scope{TenantID: "t1"}, "de", routeReq)
	if err != nil {
		t.Fatal(err)
	}
	if out.Route.Provider != "local" || out.Priced || len(out.Attempts) != 2 || out.Attempts[0].Error == "" {
		t.Errorf("routed = %+v", out)
	}
	// The failed attempt's billed tokens are still recorded.
	if budget.Spent("t1") != 3000 || len(budget.Spends()) != 2 {
		t.Errorf("spends = %+v", budget.Spends())
	}
}

func TestRouterStops(t *testing.T) {
	scope := domain.Scope{TenantID: "t1"}
	t.Run("budget exceeded never falls back", func(t *testing.T) {
		r, primary, _, _ := routerFixture(nil)
		_, err := r.Complete(context.Background(), domain.Scope{TenantID: "no-cap"}, "de", routeReq)
		if !errors.Is(err, domain.ErrBudgetExceeded) || len(primary.got) != 0 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a non-provider error stops", func(t *testing.T) {
		miss := errors.New("cassette mismatch")
		r, _, secondary, _ := routerFixture(miss)
		out, err := r.Complete(context.Background(), scope, "de", routeReq)
		if !errors.Is(err, miss) || len(secondary.got) != 0 || len(out.Attempts) != 1 {
			t.Errorf("err = %v, attempts %+v", err, out.Attempts)
		}
	})
	t.Run("all routes failed", func(t *testing.T) {
		r, _, secondary, _ := routerFixture(&domain.ProviderError{Provider: "anthropic", Kind: domain.KindAuth})
		secondary.err = &domain.ProviderError{Provider: "local", Kind: domain.KindUnavailable}
		out, err := r.Complete(context.Background(), scope, "de", routeReq)
		if !errors.Is(err, app.ErrAllRoutesFailed) || !app.Transient(err) || len(out.Attempts) != 2 {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("rejections only are not transient", func(t *testing.T) {
		r, _, secondary, _ := routerFixture(&domain.ProviderError{Provider: "anthropic", Kind: domain.KindAuth})
		secondary.err = &domain.ProviderError{Provider: "local", Kind: domain.KindInvalidRequest}
		_, err := r.Complete(context.Background(), scope, "de", routeReq)
		if err == nil || app.Transient(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no route", func(t *testing.T) {
		r, _, _, _ := routerFixture(nil)
		req := routeReq
		req.Task = domain.TaskReview
		if _, err := r.Complete(context.Background(), scope, "de", req); !errors.Is(err, domain.ErrNoRoute) {
			t.Errorf("err = %v", err)
		}
	})
}
